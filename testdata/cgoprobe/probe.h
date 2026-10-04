// The C side of cgoprobe. probe.c defines these. Each exercises one part of the C runtime.

int probe_add(int a, int b);
int probe_printf(const char *word);
int probe_errno(void);
int probe_callback(int value);
int probe_thread(int value);
