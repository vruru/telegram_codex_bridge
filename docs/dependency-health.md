# Dependency Health Report

**Date:** 2026-10-05
**Stack:** Go backend (`go.mod` / `go.sum`), native Swift AppKit UI (no Swift package manifest).

## Audit Baseline

Govulncheck v1.8.0 (DB modified 2026-10-01) detected 34 unique version-matched advisories: 33 stdlib, 1 x/sys. Detection includes 17 symbol-level (static call-graph evidence, not proof of exploitability) and 17 module-only. Severity uses CISA-ADP CVSS3.1 in the [CVE Program records](https://cveawg.mitre.org/api/cve/CVE-2026-39821) (other assessors may score differently): 1 Critical, 18 High, 13 Medium, 2 Low. SQLite 3.51.2 CVEs (CVE-2026-11822, CVE-2026-11824, High, CVSS4.0 8.5) affect FTS5, which the project does not use; the project does not accept arbitrary SQL or import untrusted databases. These two CVE IDs describe the same issue, according to [SQLite's CVE page](https://sqlite.org/cves.html), and are not in govulncheck results.

## Chosen Upgrades

1. **Go 1.26.0 → 1.26.6:** Minimal patch resolving all baseline stdlib advisories.
2. **modernc.org/sqlite 1.46.1 → 1.52.0:** First driver with SQLite 3.53.2 (fixes FTS5 bugs, preemptive).
3. **modernc.org/libc 1.67.6 → 1.72.3:** Exact requirement for new sqlite driver.
4. **golang.org/x/sys 0.37.0 → 0.44.0:** Minimum fix for GO-2026-5024 (Windows-only).
5. **github.com/coder/websocket 1.8.14 → 1.8.15:** Compatible patch, preventive (no known finding).

`go mod tidy` marks websocket direct and removes unused x/exp. SQLite's [v1.52.0 changelog](https://gitlab.com/cznic/sqlite/-/blob/v1.52.0/CHANGELOG.md) identifies the first release with engine 3.53.2. Go's [release history](https://go.dev/doc/devel/release#go1.26) documents the standard-library fixes. No major upgrades were needed.

The lock graph also updates these transitive modules:

| Module | Before | After |
| --- | --- | --- |
| golang.org/x/mod | v0.29.0 | v0.33.0 |
| golang.org/x/sync | v0.17.0 | v0.20.0 |
| golang.org/x/tools | v0.38.0 | v0.42.0 |
| modernc.org/cc/v4 | v4.27.1 | v4.28.2 |
| modernc.org/ccgo/v4 | v4.30.1 | v4.34.0 |
| modernc.org/fileutil | v1.3.40 | v1.4.0 |
| modernc.org/gc/v3 | v3.1.1 | v3.1.2 |
| modernc.org/opt | v0.1.4 | v0.2.0 |

## Advisory Table

| Advisory | CVE | Severity(CVSS3.1) | Detection level | Minimum fixed Go version (except x/sys) |
| :--- | :--- | :--- | :--- | :--- |
| [GO-2026-4599](https://pkg.go.dev/vuln/GO-2026-4599) | CVE-2026-27137 | HIGH (7.5) | symbol | v1.26.1 |
| [GO-2026-4600](https://pkg.go.dev/vuln/GO-2026-4600) | CVE-2026-27138 | MEDIUM (5.9) | symbol | v1.26.1 |
| [GO-2026-4601](https://pkg.go.dev/vuln/GO-2026-4601) | CVE-2026-25679 | HIGH (7.5) | symbol | v1.26.1 |
| [GO-2026-4602](https://pkg.go.dev/vuln/GO-2026-4602) | CVE-2026-27139 | LOW (2.5) | symbol | v1.26.1 |
| [GO-2026-4603](https://pkg.go.dev/vuln/GO-2026-4603) | CVE-2026-27142 | MEDIUM (6.1) | module | v1.26.1 |
| [GO-2026-4864](https://pkg.go.dev/vuln/GO-2026-4864) | CVE-2026-32282 | MEDIUM (6.4) | module | v1.26.2 |
| [GO-2026-4865](https://pkg.go.dev/vuln/GO-2026-4865) | CVE-2026-32289 | MEDIUM (6.1) | module | v1.26.2 |
| [GO-2026-4866](https://pkg.go.dev/vuln/GO-2026-4866) | CVE-2026-33810 | HIGH (7.5) | symbol | v1.26.2 |
| [GO-2026-4869](https://pkg.go.dev/vuln/GO-2026-4869) | CVE-2026-32288 | MEDIUM (5.5) | module | v1.26.2 |
| [GO-2026-4870](https://pkg.go.dev/vuln/GO-2026-4870) | CVE-2026-32283 | HIGH (7.5) | symbol | v1.26.2 |
| [GO-2026-4918](https://pkg.go.dev/vuln/GO-2026-4918) | CVE-2026-33814 | HIGH (7.5) | symbol | v1.26.3 |
| [GO-2026-4946](https://pkg.go.dev/vuln/GO-2026-4946) | CVE-2026-32281 | HIGH (7.5) | symbol | v1.26.2 |
| [GO-2026-4947](https://pkg.go.dev/vuln/GO-2026-4947) | CVE-2026-32280 | HIGH (7.5) | symbol | v1.26.2 |
| [GO-2026-4970](https://pkg.go.dev/vuln/GO-2026-4970) | CVE-2026-39822 | HIGH (7.8) | module | v1.26.5 |
| [GO-2026-4971](https://pkg.go.dev/vuln/GO-2026-4971) | CVE-2026-39836 | HIGH (7.5) | symbol | v1.26.3 |
| [GO-2026-4976](https://pkg.go.dev/vuln/GO-2026-4976) | CVE-2026-39825 | MEDIUM (5.3) | module | v1.26.3 |
| [GO-2026-4977](https://pkg.go.dev/vuln/GO-2026-4977) | CVE-2026-42499 | HIGH (7.5) | module | v1.26.3 |
| [GO-2026-4980](https://pkg.go.dev/vuln/GO-2026-4980) | CVE-2026-39826 | MEDIUM (6.1) | module | v1.26.3 |
| [GO-2026-4981](https://pkg.go.dev/vuln/GO-2026-4981) | CVE-2026-33811 | HIGH (7.5) | module | v1.26.3 |
| [GO-2026-4982](https://pkg.go.dev/vuln/GO-2026-4982) | CVE-2026-39823 | MEDIUM (6.1) | module | v1.26.3 |
| [GO-2026-4986](https://pkg.go.dev/vuln/GO-2026-4986) | CVE-2026-39820 | HIGH (7.5) | module | v1.26.3 |
| [GO-2026-5024](https://pkg.go.dev/vuln/GO-2026-5024) | CVE-2026-39824 | LOW (3.3) | module | v0.44.0 |
| [GO-2026-5026](https://pkg.go.dev/vuln/GO-2026-5026) | CVE-2026-39821 | CRITICAL (9.6) | symbol | v1.26.6 |
| [GO-2026-5037](https://pkg.go.dev/vuln/GO-2026-5037) | CVE-2026-27145 | MEDIUM (6.5) | symbol | v1.26.4 |
| [GO-2026-5038](https://pkg.go.dev/vuln/GO-2026-5038) | CVE-2026-42504 | HIGH (7.5) | module | v1.26.4 |
| [GO-2026-5039](https://pkg.go.dev/vuln/GO-2026-5039) | CVE-2026-42507 | MEDIUM (5.3) | symbol | v1.26.4 |
| [GO-2026-5856](https://pkg.go.dev/vuln/GO-2026-5856) | CVE-2026-42505 | MEDIUM (5.3) | symbol | v1.26.5 |
| [GO-2026-5942](https://pkg.go.dev/vuln/GO-2026-5942) | CVE-2026-46600 | HIGH (7.5) | module | v1.26.6 |
| [GO-2026-5972](https://pkg.go.dev/vuln/GO-2026-5972) | CVE-2026-33818 | HIGH (7.5) | symbol | v1.26.6 |
| [GO-2026-6088](https://pkg.go.dev/vuln/GO-2026-6088) | CVE-2026-56859 | HIGH (7.5) | module | v1.26.6 |
| [GO-2026-6089](https://pkg.go.dev/vuln/GO-2026-6089) | CVE-2026-56853 | HIGH (7.5) | module | v1.26.6 |
| [GO-2026-6090](https://pkg.go.dev/vuln/GO-2026-6090) | CVE-2026-56862 | HIGH (7.5) | symbol | v1.26.6 |
| [GO-2026-6091](https://pkg.go.dev/vuln/GO-2026-6091) | CVE-2026-56858 | MEDIUM (6.1) | module | v1.26.6 |
| [GO-2026-6218](https://pkg.go.dev/vuln/GO-2026-6218) | CVE-2026-56860 | MEDIUM (5.9) | symbol | v1.26.6 |

## Validation

- PASS: `go vet ./...`
- PASS: `go test -race -count=1 ./...`
- PASS: `go build ./...`
- PASS: `go mod verify`
- PASS: `go mod tidy -diff`
- PASS: govulncheck source scans (darwin/arm64 and linux/amd64) — no vulnerabilities found; module scan — zero version-matched findings
- PASS: `scripts/build-release-archives.sh` (linux/darwin, amd64/arm64, `CGO_ENABLED=0`)

Swift app/icon generation, DMG packaging, and UPX compression were skipped. Cross-compiled Linux binaries were built but not executed on this macOS host. Existing installed binaries were not redeployed.

The in-repository temporary directory exposed an existing test failure for local Markdown file links containing spaces. The shared link matcher now accepts spaces within a single-line path, and regression tests cover artifact discovery and absolute-path sanitization. The full test suite was rerun after the fix.
