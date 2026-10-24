# R6 dependency-advisory triage

## Disposition

Triage complete. The owner explicitly accepted the known Pi dependency risk for release **0.1.0**, authorized integration/release, and retained R6 / issue #137 for further improvements. The vulnerabilities are **not fixed** and there is no permanent waiver. The earlier security gate was blocked pending this owner decision; that release-specific decision has now been supplied. The Darwin local-workflow pass and documentation review remain valid. Repeating them does not remediate the dependency.

Inline review under the existing no-child authority. No install, application execution, exploit attempt, source/dependency change or credential access.

## Observed dependency identity

`pi/package-lock.json` pins `@earendil-works/pi-coding-agent` 1.4.0, whose dependencies pin `undici` **8.10.2**. This is a production dependency, not a development-only tool.

An actual `npm audit --json --package-lock-only --ignore-scripts` query against the public npm registry completed with exit 1 and no audit error. The query used copies of the public package/lock files in an isolated temporary directory, an isolated HOME/cache and distinct empty user/global npm configuration files. Repository files and installed packages were not modified.

npm reports **two affected dependency rows** (Undici high, Pi moderate through Undici), but **three distinct upstream advisories**. These counts are different concepts, not five independent vulnerabilities.

Raw audit output and stderr are retained at `/tmp/timesheet-r6-advisory-e93yka7u/`. These are this investigation's observations, not a reuse of the earlier Mac install warning.

## Findings and reachability

| Advisory | Severity | Upstream trigger | Project disposition |
| --- | --- | --- | --- |
| [GHSA-xjj6-f33q-929c](https://github.com/advisories/GHSA-xjj6-f33q-929c) | High | Malicious HTTP response with excessive content encodings and early connection close can terminate the Node process despite catching the fetch rejection. | Vulnerable fetch implementation ships in the Pi runtime; static CLI setup configures Undici and installs its globals. No basis to classify this as dev-only or unreachable. Treat as potentially reachable during network/model operation; no exploit was run. |
| [GHSA-4qv8-2p9j-2685](https://github.com/advisories/GHSA-4qv8-2p9j-2685) | Moderate | Undici EventSource parser retains unbounded state from malicious SSE responses. | Vulnerable parser ships. An application-specific EventSource construction path was not established; fetch-based model streaming alone is not proof that this EventSource parser is used. Not waived. |
| [GHSA-f72v-6w45-q472](https://github.com/advisories/GHSA-f72v-6w45-q472) | Moderate | Uppercase/mixed-case fetch integrity algorithm tokens can bypass response-body integrity validation. | Vulnerable fetch ships. An application call supplying such integrity metadata was not established. This is not evidence that npm lockfile integrity validation is bypassed. Not waived. |

All three advisories affect 8.10.2; their upstream patched version in the 8.x line is **8.12.2**. The upstream fetch-crash and EventSource advisories list no workaround. The integrity advisory's lowercase-token workaround does not address the other vulnerabilities.

## Static evidence

- Installed SDK `dist/cli/setup.js:8-10` invokes `configureHttpDispatcher` before provider requests.
- SDK `dist/core/http-dispatcher.js:73-95` creates its Undici dispatcher, sets it globally and, when the existing fetch has not been deliberately replaced, invokes `undici.install()`.
- SDK package metadata selects `dist/bundle/cli.js` as the CLI executable. `dist/bundle/chunks/chunk-MM3LKT73.js` contains bundled Undici fetch and EventSource implementations as well as dispatcher code. The installed unbundled modules provide readable corroborating source, not a separately executed runtime proof.
- The project launcher selects its own installed `@earendil-works/pi-coding-agent` tree, not an unrelated globally installed Pi.

Reachability conclusions above are static assessments. No malicious endpoint, authenticated model request or process-crash reproduction was executed. The existing Go submission/durability guarantees are not disproved by these advisories; this is a Pi-process availability/integrity dependency gate.

## Recommended next gate

Authorize a narrowly scoped dependency-remediation packet. Select an exact Pi artifact whose **shipped runtime bundle**, not merely its external lockfile dependency, contains the upstream fixes for all three advisories. Preserve the adapter's isolation, tool boundary, transport and human approval semantics. Revalidate the changed artifact and its affected HTTP/adapter paths; do not restart the entire R1–R6 verification cycle without a concrete reason.

Do not run `npm audit fix --force`: npm's suggested change here is a major downgrade of Pi to 0.75.3, not a demonstrated compatible repair. A lockfile override alone is also insufficient proof because the selected CLI is prebundled. No claim is made that a particular patched Pi release has been found or tested.

Current pins remain unchanged until that scope is approved. Final R6 acceptance remains open; no implicit risk acceptance follows from the passing offline/local Darwin workflow.
