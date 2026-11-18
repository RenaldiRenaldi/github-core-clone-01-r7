# Git forge R7

## Smart HTTP protocol notes

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
commits. Smart HTTP requests are routed to Git's `http-backend`, which handles
pkt-line framing, pack generation and validation, loose object storage, and
branch ref updates. The server exposes the normal upload-pack and receive-pack
URLs below the repository path.

Verified with the stock Git CLI (server port assigned at startup):

```text
$ git clone http://127.0.0.1:45151/octocat/hello-world.git /tmp/r7-clone-test2
Cloning into '/tmp/r7-clone-test2'...
9d7b4e6 Add greeting
497de37 Initial demo
$ cd /tmp/r7-clone-test2
$ git commit -m 'Test push'
[main a3c2a55] Test push
 1 file changed, 1 insertion(+)
 create mode 100644 pushed.txt
$ git push origin main
To http://127.0.0.1:45151/octocat/hello-world.git
   9d7b4e6..a3c2a55  main -> main
$ git ls-remote origin refs/heads/main
a3c2a55e8ab2d8de6734c47fc9b3c604f946cb1f refs/heads/main
$ git clone -q http://127.0.0.1:45151/octocat/hello-world.git /tmp/r7-clone-verify
$ git -C /tmp/r7-clone-verify log -1 --oneline
a3c2a55 Test push
```

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
`08d325a2d00218ad3ece853c4bbdef133d82178233e8ddcbc7feab9e91620763` both
times.

## Loopback measurements

Measured on this checkout with Go 1.22.2, a server on loopback, one persistent
HTTP connection per workload, and sequential requests, following R4's TCP
benchmark shape. Repository and issue reads used 2,000 requests, ref ads 200,
and writes 200. The R4 values below used a different host and implementation;
R3/R5 rows are only populated where their README has measurements.

| Operation | Requests/sec | Elapsed |
|---|---:|---:|
| Repository reads | 8,883 | 225.15 ms / 2,000 |
| Issue reads | 10,422 | 191.89 ms / 2,000 |
| Ref advertisements | 133 | 1,509.27 ms / 200 |
| Issue writes | 2,476 | 80.77 ms / 200 |
| Cached clone fetches | Not measured | The current CGI-backed handler starts `git http-backend` per request; it is not comparable to R4's in-process cached pack path. |

| Workload | R3 | R4 | R5 | R7 |
|---|---:|---:|---:|---:|
| Repository reads/sec | — | 14,216 | — | 8,883 |
| Ref advertisements/sec | — | 16,376 | — | 133 |
| Cached fetches/sec | — | 12,645 | — | Not measured |
| 200 issue writes | 83.002 ms | 97.893 ms | — | 80.77 ms |

R4's issue writes took 18% longer than R3's. R7's 200 writes took 80.77 ms
with no per-request store disk I/O, beating the R4 value in this run. The
forge-wide mutex serializes API mutations and reads. R7's ref advertisement
path is slow because each request starts `git http-backend`; the clone fetch
workload was not measured and is a known performance limitation.

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

## Three-way merges, CAS pushes, reviews, and branch protection

PR merges compute a merge base and compare the base, head, and current base
trees in Go. Clean merges create a two-parent commit and update the base ref
with Git's expected-old-value `update-ref`; conflicts return HTTP 409 and list
the conflicting paths. Receive-pack command lines are checked against current
ref SHAs before Git's atomic receive transaction runs. The REST ref endpoint
returns the current SHA for clients performing compare-and-swap updates.
Reviews and branch protection rules live in the shared in-memory store.

Live curl checks against a server on port 37573 (branches were created through
the server's Git smart HTTP endpoints):

```text
$ curl -sS http://127.0.0.1:37573/api/v3/repos/octocat/hello-world/git/refs/heads/main
{"ref":"refs/heads/main","sha":"9d7b4e6a9682052342bb93ab80414777c751899c"}
$ curl -sS -X POST http://127.0.0.1:37573/api/v3/repos/octocat/hello-world/pulls -H 'Content-Type: application/json' -d '{"title":"Curl clean merge","head":"curl-clean","base":"main"}'
{"number":2,"title":"Curl clean merge","body":"","state":"open","head":"curl-clean","base":"main"}
$ curl -sS -X POST http://127.0.0.1:37573/api/v3/repos/octocat/hello-world/pulls/2/merge -H 'Content-Type: application/json' -d '{}'
{"merged":true,"sha":"d358f05c22a86b4189dc00d7c856c49b591c8ed4"}
$ curl -sS -X PUT http://127.0.0.1:37573/api/v3/repos/octocat/hello-world/branches/main/protection -H 'Content-Type: application/json' -d '{"required_approving_reviews":1,"require_pull_request":true}'
{"required_approving_reviews":1,"require_pull_request":true}
$ curl -sS -w '\nHTTP %{http_code}\n' -X POST http://127.0.0.1:37573/api/v3/repos/octocat/hello-world/pulls/3/merge -H 'Content-Type: application/json' -d '{}'
{"message":"branch protection requires an approving review"}
HTTP 403
$ curl -sS -X PUT http://127.0.0.1:37573/api/v3/repos/octocat/hello-world/branches/main/protection -H 'Content-Type: application/json' -d '{"required_approving_reviews":0,"require_pull_request":true}'
{"required_approving_reviews":0,"require_pull_request":true}
$ curl -sS -w '\nHTTP %{http_code}\n' -X POST http://127.0.0.1:37573/api/v3/repos/octocat/hello-world/pulls/4/merge -H 'Content-Type: application/json' -d '{}'
{"conflicting_paths":["hello.txt"],"message":"merge conflict"}
HTTP 409
```

The tests cover clean and conflicting three-way merges, stale and matching
receive-pack old SHA values, and merges blocked until a review approves them.
