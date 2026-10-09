package main

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"sync/atomic"
	"syscall"
	"time"
	"unsafe"
)

// The process-control surface: the CPU mask, the priority class, and the
// stop/continue pair. These are what a supervisor reaches for when it has to
// keep a machine from being oversubscribed, so a host that cannot serve them
// makes the caller think it throttled something it never touched.
//
// A host with no call says so itself, and that answer passes here. A host that
// has one has to perform it: the mask has to come back from the kernel, the
// priority has to move, and a stopped process has to still be there.

// cpusetWords is the width of a cpu_set_t: the Linux ABI both calls are
// spelled against, and the shape this layer passes through.
const cpusetWords = 16

// prioProcess is PRIO_PROCESS, the only selection the priority calls take
// here. The syscall package names it for Linux alone, and this probe is built
// for the host it runs on.
const prioProcess = 0

// priorityNice is the value the priority check moves a child to.
const priorityNice = 19

// procControlSettle is how long a stopped child is given to finish any write
// already in flight before its output is watched for silence.
const procControlSettle = 150 * time.Millisecond

// procControlQuiet is how long a stopped child's output is watched.
const procControlQuiet = 400 * time.Millisecond

func checkProcControl() {
	const name = "proccontrol"
	var notes []string

	affinity, why := procControlAffinity()
	if why != "" {
		fail(name, "%s", why)
		return
	}
	if affinity != "" {
		notes = append(notes, affinity)
	}

	child, _, bad := selfCommand(name, "proccontrol")
	if bad {
		return
	}
	stdout, err := child.StdoutPipe()
	if err != nil {
		fail(name, "StdoutPipe: %v", err)
		return
	}
	if err := child.Start(); err != nil {
		fail(name, "start the child: %v", err)
		return
	}
	// A stop the host ignores leaves the child spinning, so it is killed
	// rather than waited for.
	defer func() {
		// A stopped child is still a child, and this one loops forever, so it
		// is ended rather than waited for.
		_ = child.Process.Kill()
		_ = child.Wait()
	}()

	ticks := newTickCounter(stdout)
	if !ticks.await(20 * time.Second) {
		fail(name, "the child printed no tick line: %s", ticks.detail())
		return
	}
	pid := child.Process.Pid

	if why := procControlPriority(pid); why != "" {
		fail(name, "%s", why)
		return
	}
	notes = append(notes, fmt.Sprintf("priority on pid %d moved", pid))

	if why := procControlStop(pid, ticks); why != "" {
		fail(name, "%s", why)
		return
	}
	notes = append(notes, "a stopped process stopped and resumed")

	ok(name, strings.Join(notes, "; "))
}

// procControlAffinity pins the caller to one of the CPUs it is already allowed
// on, reads the mask back from the kernel, and restores what it found. It
// returns a note for the report, or the reason a call failed.
func procControlAffinity() (string, string) {
	before, err := affinityMask(0)
	if errors.Is(err, syscall.ENOSYS) {
		// No per-process CPU mask here, and the host says so itself.
		return "", ""
	}
	if err != nil {
		return "", fmt.Sprintf("sched_getaffinity: %v", err)
	}
	if len(before) == 0 {
		return "", "the CPU mask came back empty"
	}
	want := before[0]
	if err := setAffinity(0, []int{want}); err != nil {
		return "", fmt.Sprintf("sched_setaffinity(pid 0, cpu %d): %v", want, err)
	}
	defer func() {
		// The child checks below inherit this mask, so it goes back either
		// way.
		setAffinity(0, before)
	}()
	after, err := affinityMask(0)
	if err != nil {
		return "", fmt.Sprintf("sched_getaffinity after set: %v", err)
	}
	if len(after) != 1 || after[0] != want {
		return "", fmt.Sprintf("the mask came back as %v, want [%d]", after, want)
	}
	return fmt.Sprintf("the CPU mask round-tripped as cpu %d", want), ""
}

// procControlPriority moves a spawned child's priority and reads it back.
func procControlPriority(pid int) string {
	before, err := syscall.Getpriority(prioProcess, pid)
	if err != nil {
		return fmt.Sprintf("getpriority: %v", err)
	}
	if err := syscall.Setpriority(prioProcess, pid, priorityNice); err != nil {
		return fmt.Sprintf("setpriority: %v", err)
	}
	after, err := syscall.Getpriority(prioProcess, pid)
	if err != nil {
		return fmt.Sprintf("getpriority after set: %v", err)
	}
	if after != before {
		return ""
	}
	// The child already ran at the top of the range.
	err = syscall.Setpriority(prioProcess, pid, priorityNice-1)
	if errors.Is(err, syscall.EACCES) || errors.Is(err, syscall.EPERM) {
		return ""
	}
	if err != nil {
		return fmt.Sprintf("setpriority below nice %d: %v", priorityNice, err)
	}
	lowered, err := syscall.Getpriority(prioProcess, pid)
	if err != nil {
		return fmt.Sprintf("getpriority after a lower set: %v", err)
	}
	if lowered == after {
		return fmt.Sprintf("the priority stayed at %d", after)
	}
	return ""
}

// procControlStop suspends a child, watches for silence, resumes it, and
// watches for it to start again. A host whose stop signal ends the process
// instead fails at the resume, which is the whole point of the check.
func procControlStop(pid int, ticks *tickCounter) string {
	if err := syscall.Kill(pid, syscall.SIGSTOP); err != nil {
		return fmt.Sprintf("kill(SIGSTOP): %v", err)
	}
	// A stopped process is still a process: a supervisor that finds it gone
	// has lost the job it was trying to hold back.
	if err := syscall.Kill(pid, 0); err != nil {
		return fmt.Sprintf("the stopped process is gone: kill(pid, 0) said %v", err)
	}
	time.Sleep(procControlSettle)
	held := ticks.count()
	time.Sleep(procControlQuiet)
	if grew := ticks.count() - held; grew > 0 {
		return fmt.Sprintf("a stopped process printed %d more tick lines", grew)
	}
	if err := syscall.Kill(pid, syscall.SIGCONT); err != nil {
		return fmt.Sprintf("kill(SIGCONT): %v", err)
	}
	if !ticks.await(5 * time.Second) {
		return fmt.Sprintf("the resumed process printed nothing: %s", ticks.detail())
	}
	if err := ticks.err(); err != nil {
		return fmt.Sprintf("reading the child's output: %v", err)
	}
	return ""
}

// tickCounter counts the lines a child prints while it runs.
type tickCounter struct {
	lines   int64
	last    atomic.Pointer[string]
	readErr atomic.Pointer[error]
	done    chan struct{}
}

// newTickCounter drains r until it ends, counting lines.
func newTickCounter(r io.Reader) *tickCounter {
	t := &tickCounter{done: make(chan struct{})}
	go func() {
		defer close(t.done)
		scanner := bufio.NewScanner(r)
		for scanner.Scan() {
			line := scanner.Text()
			t.last.Store(&line)
			if strings.HasPrefix(line, "tick") {
				atomic.AddInt64(&t.lines, 1)
			}
		}
		if err := scanner.Err(); err != nil {
			t.readErr.Store(&err)
		}
	}()
	return t
}

func (t *tickCounter) count() int64 { return atomic.LoadInt64(&t.lines) }

func (t *tickCounter) err() error {
	if p := t.readErr.Load(); p != nil {
		return *p
	}
	return nil
}

// detail names the last line a silent child printed, which is what tells "it
// never started" from "it stopped talking".
func (t *tickCounter) detail() string {
	if p := t.last.Load(); p != nil {
		return "last line " + *p
	}
	return "no output at all"
}

// await waits for one more line than the counter holds.
func (t *tickCounter) await(timeout time.Duration) bool {
	held := t.count()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if t.count() > held {
			return true
		}
		time.Sleep(5 * time.Millisecond)
	}
	return false
}

// affinityMask reads a process's CPU set with sched_getaffinity(2).
func affinityMask(pid int) ([]int, error) {
	var mask [cpusetWords]uint64
	n, _, errno := syscall.Syscall(syscall.SYS_SCHED_GETAFFINITY, uintptr(pid),
		unsafe.Sizeof(mask), uintptr(unsafe.Pointer(&mask[0])))
	if errno != 0 {
		return nil, errno
	}
	width := int(n / 8)
	if width <= 0 || width > cpusetWords {
		width = cpusetWords
	}
	var out []int
	for word := 0; word < width; word++ {
		for bit := 0; bit < 64; bit++ {
			if mask[word]&(1<<uint(bit)) != 0 {
				out = append(out, word*64+bit)
			}
		}
	}
	return out, nil
}

// setAffinity restricts a process to the given CPUs with
// sched_setaffinity(2).
func setAffinity(pid int, cpus []int) error {
	var mask [cpusetWords]uint64
	for _, c := range cpus {
		if c < 0 || c >= cpusetWords*64 {
			return syscall.EINVAL
		}
		mask[c/64] |= 1 << uint(c%64)
	}
	_, _, errno := syscall.Syscall(syscall.SYS_SCHED_SETAFFINITY, uintptr(pid),
		unsafe.Sizeof(mask), uintptr(unsafe.Pointer(&mask[0])))
	if errno != 0 {
		return errno
	}
	return nil
}

// procControlChild is the RUNTIMEPROBE_CHILD=proccontrol mode: print a tick
// line on a fixed cadence until someone stops the process or ends it. The
// cadence is what makes a stop visible from the outside, and the process never
// exits on its own so a resumed one is still there to print again.
func procControlChild() {
	fmt.Println("proccontrol-child", os.Getpid())
	for i := 0; ; i++ {
		fmt.Println("tick", i)
		time.Sleep(10 * time.Millisecond)
	}
}
