# Git forge R7

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
$ git clone http://127.0.0.1:45335/octocat/hello-world.git /tmp/r7-no-git-clone
Cloning into '/tmp/r7-no-git-clone'...
9d7b4e6 Add greeting
497de37 Initial demo
$ git -C /tmp/r7-no-git-clone -c user.name=Fix2 -c user.email=fix2@example.test commit --allow-empty -m 'pure Go receive-pack smoke test'
[main 8f4a5d7] pure Go receive-pack smoke test
$ git push origin main
To http://127.0.0.1:45335/octocat/hello-world.git
   9d7b4e6..8f4a5d7  main -> main
$ git ls-remote origin refs/heads/main
8f4a5d78310eabb7a6bc2735695c027644c5c33a refs/heads/main
$ git clone -q http://127.0.0.1:45335/octocat/hello-world.git /tmp/r7-no-git-fresh
$ git -C /tmp/r7-no-git-fresh log -1 --oneline
8f4a5d7 pure Go receive-pack smoke test
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

Benchmarks use one persistent HTTP connection and sequential requests, against
the pure-Go server built for this revision. The Git upload path still traverses
and compresses the object graph for each fetch; it does not yet cache pack
bytes. The R3/R4 comparison figures came from their README records and are
historical, not same-host measurements.

The kit's grade-local run and final transcript checks are recorded below.

## Grade-local and final transcript checks

The committed static executable scored **425/1000**, with 35/120 checks passed,
on the kit's process driver at commit `f312717`. The run reproduced byte for
byte across live and replay. Its four loopback workloads measured pushes,
reads, and writes; shallow clone fetches failed because the server does not yet
implement shallow negotiation. The kit's Docker driver could not start on this
VM: `spawnSync docker ENOENT`. The process run used Node 24.22.1 while the kit
specifies Node 26.13.1. This is below the 950-point target.

The nine-session bundle (test, build-1, core, smart HTTP, API, merges/final,
Fix 1, and Fix 2 sessions) is
`/home/hatch/workspace/makefun/github-core-clone-01-r7-fix2-final.tar.gz`,
SHA-256 `5411592131f3bb3b4cb793974e260e20bd7e03543262a8d74d6f6e3806c7aab5`.
The kit check against this bundle passed with no failed checks:
`thinking_reproduces` passed at 32.0%, `thinking_priced` passed with 433 usage
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
