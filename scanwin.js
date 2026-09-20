// Scan recent gosmopolitan workflow runs for the windows shard-0 suite job and
// report which ones failed with the empty-vet-output signature.
const { execFileSync } = require("node:child_process");

function gh(args) {
	return execFileSync("gh", args, { maxBuffer: 1 << 28 }).toString();
}

const runs = JSON.parse(
	gh(["api", "repos/wow-look-at-my/gosmopolitan/actions/runs?per_page=60"]),
).workflow_runs;

for (const run of runs) {
	if (run.status !== "completed") continue;
	let jobs;
	try {
		jobs = JSON.parse(
			gh([
				"api",
				`repos/wow-look-at-my/gosmopolitan/actions/runs/${run.id}/jobs?per_page=100`,
			]),
		).jobs;
	} catch (err) {
		console.log(`${run.id} jobs unavailable: ${err.message}`);
		continue;
	}
	const job = jobs.find((one) => one.name === "suite (windows-latest, 0, 2)");
	if (!job) continue;
	let mark = "";
	if (job.conclusion === "failure") {
		try {
			const log = gh([
				"api",
				"--allow-escape-sequences",
				`repos/wow-look-at-my/gosmopolitan/actions/jobs/${job.id}/logs`,
			]);
			const vet = log.includes("vet output:");
			const dead = log.includes("should be reachable");
			const dump = log.includes("dump output:");
			mark = ` vet=${vet} deadcode=${dead} inldump=${dump}`;
		} catch (err) {
			mark = " log unavailable";
		}
	}
	console.log(
		`${run.created_at} run=${run.id} sha=${run.head_sha.slice(0, 10)} job=${job.id} ${job.conclusion}${mark}`,
	);
}
