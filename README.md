# Git forge R7

## R7 Fix 5: protocol v2 shallow upload-pack

The shallow-clone failure was a protocol-version mismatch. R4 advertises
upload-pack protocol v2 when the request carries `Git-Protocol: version=2` and
answers a fetch in v2 framing. R7 ignored that header and advertised v0, so
stock Git's shallow negotiation received v0 `shallow`/flush/`NAK` packets where
it expected v2 `shallow-info`/delimiter/`packfile` sections. R7 now advertises
the v2 capabilities and handles `ls-refs` and `fetch` using the same framing
and boundary selection as R4.

The R4 v2 exchange is: advertisement capabilities then flush; `ls-refs`
request ending in flush and ref pkt-lines then flush; fetch request with a
delimiter and `deepen N`, wants, and `done`; response `shallow-info`, one
`shallow <commit>` per in-depth commit whose parents are omitted, delimiter,
`packfile`, channel-1 sideband pack bytes, flush. R7's stock-Git trace now has
that same shape. The failed R7 v0 trace had no `version 2` capability and its
depth request response began with `shallow <tip>`, flush, `NAK`; Git then
reported `expected shallow list` / `expected packfile` as it parsed the
out-of-phase response.

The regression test drives the handler with depth-1 and depth-2 v2 requests,
asserts the shallow-info marker, boundary ID, delimiter, packfile marker and
pack contents. It uses the pkt-line codec and object store without invoking
Git. The fixture has three commits: depth 1 marks the tip shallow and includes
one commit; depth 2 marks its parent shallow and includes two commits.

Verified with Go 1.24.0 (`export PATH="$HOME/bin/go/bin:$PATH"` before each Go
command): `go test ./...` and `go test -race ./...` pass. Real Git 2.43.0 depth-1
and depth-2 clones, a full clone, a commit and push, `git ls-remote`, and a
fresh full clone after push all succeed. A server built with Go and launched
with `PATH=/tmp/r7-no-git-path` returned `/healthz` `{"ok":true}`, both service
advertisements, and served a depth-1 clone with `.git/shallow` containing
`9d7b4e6a9682052342bb93ab80414777c751899c`. The server has no runtime Git
dependency; `exec.Command("git", ...)` appears only in Git-aware tests.

Protocol v2 pkt-line sequence from the working exchange:

```text
S> version 2; agent=git-core-clone/1; ls-refs=unborn; fetch=shallow filter; object-format=sha1; 0000
C> command=ls-refs; ...; 0000
S> <tip> HEAD symref-target:refs/heads/main; <tip> refs/heads/main; 0000
C> command=fetch; 0001; deepen 1; want <tip>; done; 0000
S> shallow-info; shallow <tip>; 0001; packfile; 0001<PACK bytes>; 0000
```

For the seeded tip `9d7b4e6a9682052342bb93ab80414777c751899c`, the real depth-1
clone's `.git/shallow` contains that exact ID. R4's handler uses the same
boundary rule: the depth-limited commit inside the pack is marked shallow when
it has parents beyond the requested depth.

Real Git commands and output against the R7 server:

```text
$ git clone --depth 1 http://127.0.0.1:36047/octocat/hello-world.git /tmp/r7-depth1-v2c
Cloning into '/tmp/r7-depth1-v2c'...
$ cat /tmp/r7-depth1-v2c/.git/shallow
9d7b4e6a9682052342bb93ab80414777c751899c
$ git clone --depth 2 http://127.0.0.1:36047/octocat/hello-world.git /tmp/r7-depth2
Cloning into '/tmp/r7-depth2'...
$ git -C /tmp/r7-depth2 rev-list --count HEAD
2
$ git clone http://127.0.0.1:36047/octocat/hello-world.git /tmp/r7-full
Cloning into '/tmp/r7-full'...
9d7b4e6 Add greeting
497de37 Initial demo
$ git -C /tmp/r7-full -c user.name=Fix5 -c user.email=fix5@example.test commit --allow-empty -m 'fix5 push verification'
[main b835598] fix5 push verification
$ git -C /tmp/r7-full push origin main
   9d7b4e6..b835598  main -> main
$ git ls-remote http://127.0.0.1:36047/octocat/hello-world.git refs/heads/main
b835598123c40149cadffed8a9a9c816ca0b2c64 refs/heads/main
$ git clone -q http://127.0.0.1:36047/octocat/hello-world.git /tmp/r7-fresh-after-push
$ git -C /tmp/r7-fresh-after-push log -1 --format='%H %s'
b835598123c40149cadffed8a9a9c816ca0b2c64 fix5 push verification
```

Empty-PATH startup evidence (server built with Go 1.24.0 first):

```text
$ env PATH=/tmp/r7-no-git-path /tmp/r7-fix5-server
[::]:43991
$ curl -fsS http://127.0.0.1:43991/healthz
{"ok":true}
$ curl -fsS -H 'Git-Protocol: version=2' 'http://127.0.0.1:43991/octocat/hello-world.git/info/refs?service=git-upload-pack'
000eversion 2
001bagent=git-core-clone/1
0013ls-refs=unborn
0019fetch=shallow filter
0017object-format=sha1
0000
$ curl -fsS 'http://127.0.0.1:43991/octocat/hello-world.git/info/refs?service=git-receive-pack'
0000001f# service=git-receive-pack
... refs/heads/main
0000
$ git clone --depth 1 http://127.0.0.1:43991/octocat/hello-world.git /tmp/r7-depth1-empty-path
Cloning into '/tmp/r7-depth1-empty-path'...
$ cat /tmp/r7-depth1-empty-path/.git/shallow
9d7b4e6a9682052342bb93ab80414777c751899c
```

## Smart HTTP protocol notes

## R7 Fixes 1 and 2: pure-Go Git transport

Startup no longer invokes the Git executable. `seedDemo()` writes blobs, trees,
and the two demo commits through the in-memory object store, computes their
canonical SHA-1 object IDs, and publishes the same zlib loose objects and
`refs/heads/main` from Go. Smart HTTP advertisements and packet framing are
implemented in Go. Upload-pack builds version 2 packfiles from the wanted
commit graph and excludes objects reachable from client `have` lines.
Receive-pack reads full and delta pack entries, checks the pack checksum,
stages objects, verifies compare-and-swap and fast-forward updates, and updates
refs together. The server has no Git executable dependency; pull request merge
requests return an explicit unsupported-build response.

Verified with the server process launched using an empty PATH:

```text
$ PATH=/tmp/r7-no-git-path /tmp/r7-no-git-server
[::]:41295
$ /usr/bin/curl -fsS http://127.0.0.1:41295/healthz
{"ok":true}
$ /usr/bin/curl -fsS 'http://127.0.0.1:41295/octocat/hello-world.git/info/refs?service=git-upload-pack'
001e# service=git-upload-pack
0000009d9d7b4e6a9682052342bb93ab80414777c751899c refs/heads/main\0multi_ack multi_ack_detailed thin-pack side-band side-band-64k ofs-delta agent=git-core-clone/1
0000
$ /usr/bin/curl -fsS 'http://127.0.0.1:41295/octocat/hello-world.git/info/refs?service=git-receive-pack'
001f# service=git-receive-pack
0000009d9d7b4e6a9682052342bb93ab80414777c751899c refs/heads/main\0multi_ack multi_ack_detailed thin-pack side-band side-band-64k ofs-delta agent=git-core-clone/1
0000
```

Both services advertise the seeded `Add greeting` commit
`9d9d7b4e6a9682052342bb93ab80414777c751899c`. `go test ./...` and
`go test -race ./...` pass. Real-Git tests exercise clone, push, stale-push
rejection, `ls-remote`, and a fresh clone.

The pkt-line wire format is a four hexadecimal digit length that includes its
own four bytes. `0000` is a flush packet. Git service advertisements begin
with a pkt-line containing `# service=git-upload-pack` or
`# service=git-receive-pack`, followed by a flush. For protocol v2 upload-pack,
the first pkt-line is `version 2\n`, capability lines follow as pkt-lines,
and the capability list ends with a flush. These boundaries must be preserved
through the entire HTTP response; joining payload text and adding flush markers
later can make a client interpret a newline as the next length field.

R5's regression was in the HTTP exchange framing: the client received an
incomplete pkt-line sequence and reported `invalid pkt-line length "\n000"`.
R4's working exchange encoded each line with its length prefix and put the
flush marker at the correct protocol boundary. R7's pkt-line codec now has
explicit flush, delimiter, and response-end constructors, plus regression
coverage that checks a control packet does not consume the next packet.

## Smart HTTP clone and push

The Go server seeds `octocat/hello-world.git` at startup with two deterministic
commits. Its pure-Go upload-pack and receive-pack handlers serve the normal
smart HTTP URLs below the repository path.

Verified with the stock Git CLI (server port assigned at startup):

```text
$ git clone http://127.0.0.1:44399/octocat/hello-world.git /tmp/r7-final-clone
Cloning into '/tmp/r7-final-clone'...
9d7b4e6 Add greeting
497de37 Initial demo
$ git -C /tmp/r7-final-clone -c user.name=Fix2 -c user.email=fix2@example.test commit --allow-empty -m 'final no git path push'
[main 5fbc299] final no git path push
$ git push origin main
To http://127.0.0.1:44399/octocat/hello-world.git
   9d7b4e6..5fbc299  main -> main
$ git ls-remote origin refs/heads/main
5fbc2992f0bb8cbea88cf3b5e176d84e65564b0f refs/heads/main
$ git clone -q http://127.0.0.1:44399/octocat/hello-world.git /tmp/r7-final-fresh
$ git -C /tmp/r7-final-fresh log -1 --oneline
5fbc299 final no git path push
```

The server process was built first and launched with an empty PATH using
`env PATH=/tmp/r7-no-git-path /tmp/r7-server`. `/healthz` returned
`{"ok":true}` and both smart HTTP service advertisements returned the seeded
tip. The Git CLI above ran in a separate process with the normal client PATH.

Checks: `go test ./...` and `go test -race ./...` both pass. The focused
handler tests verify the upload-pack advertisement and clone a checkout through
an in-process HTTP server.

## Deterministic state export

`GET /export` emits canonical JSON for repositories, loose refs, issues, pull
requests, reviews, and branch protection. Repository names and map keys are
sorted, issue and pull request order follows their stable creation order, and
the export contains no timestamps or generated values. Check it on a running
server with `curl -fsS http://127.0.0.1:$PORT/export | sha256sum`; two
consecutive exports of the same state returned
`6b1588378583e82398b3ca6d9803ee3f47cf6e4b1c474ea4e57ccae396ab9e8c` both
times.

## Loopback measurements

Measured on this checkout with Go 1.22.2, a server on loopback, one persistent
HTTP connection per workload, and sequential requests, following R4's TCP
benchmark shape. Repository and issue reads used 2,000 requests, ref ads 200,
and writes 200. The R4 values below used a different host and implementation;
R3/R5 rows are only populated where their README has measurements.

| Operation | Requests/sec | Elapsed |
|---|---:|---:|
| Repository reads | 4,587 | 436.02 ms / 2,000 |
| Issue reads | 7,343 | 272.38 ms / 2,000 |
| Ref advertisements | 6,310 | 31.70 ms / 200 |
| Issue writes | 6,055 | 33.03 ms / 200 |
| Cached clone fetches | 205 | 975.55 ms / 200 |

| Workload | R3 | R4 | R5 | R7 |
|---|---:|---:|---:|---:|
| Repository reads/sec | — | 14,216 | — | 4,587 |
| Ref advertisements/sec | — | 16,376 | — | 6,310 |
| Cached fetches/sec | — | 12,645 | — | 205 |
| 200 issue writes | 83.002 ms | 97.893 ms | — | 33.03 ms |

Fix 5 cached-upload measurement: 200 protocol-v2 upload-pack POSTs over one
persistent loopback HTTP connection took 95.83 ms (2,087.1 requests/sec), with
HTTP 200 for every request and a 558-byte response. The first request populated
the existing pack cache; the remaining requests reused it. The older table
above records the prior process-driver workload and remains useful for the
historical R3/R4 comparison, whose figures came from their README records and
are not same-host measurements.

The kit's grade-local run and final transcript checks are recorded below.

## Grade-local and final transcript checks

Fix 5's latest committed static-binary process grade is **490/1000** at
`8e3a99d` (41/120 feature checks; agent 133, feature 205, performance 152,
`perf_p50_us` 387,762). A previous draw of the same functional code scored
526/1000 as the loopback timings vary. L1 replay reproduced byte-for-byte. The protocol v2
advertisement, depth-1 shallow fetch, shallow new-tip fetch, and shallow deepen
checks pass. The target of 950 was not reached. The remaining failed checks
reported by the public harness are:

| Group | Failed checks |
|---|---|
| Git | `git.clone-annotated-tag`, `git.ls-refs`, `git.ls-refs-prefix`, `git.push-delete-branch`, `git.push-delete-tag`, `git.push-mixed-report` |
| GraphQL | `graphql.add-comment`, `graphql.batched-operations`, `graphql.close-issue`, `graphql.create-issue`, `graphql.create-pull-request`, `graphql.global-id-stability`, `graphql.introspection-issue`, `graphql.introspection-types`, `graphql.invalid-field`, `graphql.issue-by-number`, `graphql.issues-open-order`, `graphql.issues-pagination`, `graphql.merge-pull-request`, `graphql.node-repository`, `graphql.pull-request-files`, `graphql.pull-requests-list`, `graphql.repository-fields`, `graphql.viewer-login` |
| Issues | `issues.bad-label`, `issues.comment-create`, `issues.comments-list`, `issues.create`, `issues.create-labels`, `issues.create-title-body`, `issues.get-one`, `issues.list-open`, `issues.list-open-excludes-closed`, `issues.list-shape`, `issues.list-sort-asc`, `issues.list-sort-desc`, `issues.numbers-increment`, `issues.patch-close`, `issues.patch-close-persists` |
| MCP | `mcp.create-pull-request`, `mcp.merge-pull-request`, `mcp.parse-error`, `mcp.rest-parity`, `mcp.unknown-tool` |
| Protocol | `protocol.content-type-json`, `protocol.error-message`, `protocol.malformed-body-400`, `protocol.request-id` |
| Pull requests | `pulls.create`, `pulls.create-base-sha`, `pulls.create-body`, `pulls.create-head-sha`, `pulls.files-list`, `pulls.files-multiple`, `pulls.files-shape`, `pulls.get-one`, `pulls.get-unknown`, `pulls.list-default-open`, `pulls.list-shape`, `pulls.list-state-all`, `pulls.merge-body`, `pulls.merge-closes`, `pulls.merge-commit-on-default`, `pulls.merge-commit-sha`, `pulls.merge-conflict`, `pulls.merge-content-on-default`, `pulls.merge-status`, `pulls.merge-unknown` |
| Repositories | `repos.branches`, `repos.commits`, `repos.contents-create`, `repos.contents-delete`, `repos.contents-directory`, `repos.contents-stale-sha`, `repos.create`, `repos.delete`, `repos.pagination`, `repos.patch`, `repos.unauthorized` |

The harness export records check pass bits but no per-check assertion text.
These failures are outside upload-pack shallow negotiation; this session fixed
the shallow v2 fetch/deepen path and did not implement the broader API and ref
behaviors named above. The latest grade draw measured push 1,037.535 ms / 200
operations, clone 124.794 ms / 50, reads 938.996 ms / 2,000, and writes
185.952 ms / 200. The separate cached v2 fetch loopback measurement above was
2,087.1 requests/sec.

The Fix 5 transcript bundle is
`/home/hatch/workspace/makefun/github-core-clone-01-r7-fix5-final.tar.gz`.
Its check parsed 12 sessions, passed `thinking_reproduces` at 37.0%, passed
`thinking_priced` with 698 priced usage records, and passed `thinking_revised`;
there were no failed checks. `thinking_fresh` was a note because those sessions
do not show the newer anchor before their first counted writes. The prerequisite
check skipped Node 22 because this VM runs Node 24.22.1.

The final empty-PATH run also completed clone, push, and fresh-clone checks. A
commit `412fc0a48428394b858e967f77ef225eff99a19e` pushed to `main`, appeared in
`git ls-remote`, and was the tip of the fresh clone. Two `/export` calls both
hashed to `7a8c71bc66c5b0f110a134da6df873340b7d93d95947986e0864b9c1a1a11a62`.

Historical grade: the static executable scored **428/1000**, with 35/120 checks
passed, on the kit's process driver at commit `5ecbe1a`. The run reproduced
byte for byte across live and replay. Shallow clone fetches failed at that
revision. The kit's Docker driver could not start on this VM (`spawnSync docker
ENOENT`); the process run used Node 24.22.1 while the kit specifies Node
26.13.1. Fix 5's committed grade and transcript results are recorded below
after the final run.

The nine-session bundle (test, build-1, core, smart HTTP, API, merges/final,
Fix 1, and Fix 2 sessions) is
`/home/hatch/workspace/makefun/github-core-clone-01-r7-fix2-final.tar.gz`,
SHA-256 `18b5fb4909fa2798390983870c4bb4ce2d4b2b93f82cb012ebc8b194199653bf`.
The kit check against this bundle passed with no failed checks:
`thinking_reproduces` passed at 31.9%, `thinking_priced` passed with 468 usage
records, and `thinking_revised` passed. `thinking_fresh` was skipped as a note:
the sessions predate the newer freshness anchor. The prerequisite check skipped
Node 22 because this VM runs Node 24.22.1.

## REST, GraphQL, MCP, and web UI

The server shares one mutex-protected in-memory repository, issue, and pull
request store across REST v3, GraphQL, MCP, and `/`. Startup seeds
`octocat/hello-world` with one issue and one pull request, plus
`octocat/notes`. REST supports repository create/list/get, issue
create/list/get/update/comments, pull request create/list/get, and the demo
authenticated user. GraphQL supports `viewer` and repository issue/PR
connections. MCP advertises the eight R4 tool names; the merge, review, and
file-content operations report that they are not implemented in this step.

Live HTTP check (server started with `go run .` on port 46353):

```text
$ curl -sS -X POST http://127.0.0.1:46353/api/v3/repos/octocat/hello-world/issues -H 'Content-Type: application/json' -d '{"title":"Curl API issue","body":"Created by the live check"}'
{"number":2,"title":"Curl API issue","body":"Created by the live check","state":"open"}
$ curl -sS http://127.0.0.1:46353/api/v3/repos/octocat/hello-world/issues/2
{"number":2,"title":"Curl API issue","body":"Created by the live check","state":"open"}
$ curl -sS -X POST http://127.0.0.1:46353/graphql -H 'Content-Type: application/json' -d '{"query":"{ repository(owner: \"octocat\", name: \"hello-world\") { name owner { login } issues { nodes { number title state } } pullRequests { nodes { number title state } } } }"}'
{"data":{"repository":{"issues":{"nodes":[{"number":1,"state":"OPEN","title":"Welcome"},{"number":2,"state":"OPEN","title":"Curl API issue"}]},"name":"hello-world","owner":{"login":"octocat"},"pullRequests":{"nodes":[{"number":1,"state":"OPEN","title":"Add greeting"}]}}}
$ curl -sS -X POST http://127.0.0.1:46353/mcp -H 'Content-Type: application/json' -d '{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"create_issue","arguments":{"owner":"octocat","repo":"hello-world","title":"MCP curl issue","body":"created by MCP"}}}'
{"id":1,"jsonrpc":"2.0","result":{"content":[{"text":"{\"body\":\"created by MCP\",\"number\":3,\"state\":\"open\",\"title\":\"MCP curl issue\"}","type":"text"}],"isError":false}}
```

API tests: `go test ./...` and `go test -race ./...` pass, including issue
create/get, PR create/list, GraphQL repository selection, and an MCP tool call.

## Ref updates, reviews, and branch protection

Receive-pack validates the client's old SHA, requires branch updates to be
fast-forward, and applies all requested refs together. The REST ref endpoint
returns the current SHA. Reviews and branch protection remain in the shared
in-memory store; merge requests return a clear `not supported in this build`
response because merge object construction is outside the pure-Go transport
scope. Tests cover accepted and stale receive-pack updates and the unsupported
merge response after review protection is satisfied.
