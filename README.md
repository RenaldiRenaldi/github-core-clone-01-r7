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

## Current implementation state

This checkout contains the pkt-line codec and focused tests. The complete
forge surfaces, real-git integration run, performance measurements, and kit
transcript check have not been completed in this round.
