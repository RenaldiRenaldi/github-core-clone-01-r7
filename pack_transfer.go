package main

import (
	"bufio"
	"bytes"
	"compress/zlib"
	"crypto/sha1"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"io"
	"strings"
	"strconv"
)

var packTypeCode = map[string]byte{"commit": 1, "tree": 2, "blob": 3, "tag": 4}
var packCodeType = map[byte]string{1: "commit", 2: "tree", 3: "blob", 4: "tag"}

func (s *objectStore) reachable(roots []string) []string {
	seen, out := map[string]bool{}, []string{}
	var visit func(string)
	visit = func(id string) {
		if seen[id] {
			return
		}
		seen[id] = true
		kind, data, err := s.ReadObject(id)
		if err != nil {
			return
		}
		out = append(out, id)
		if kind == "commit" {
			head := strings.SplitN(string(data), "\n\n", 2)[0]
			for _, line := range strings.Split(head, "\n") {
				if strings.HasPrefix(line, "tree ") || strings.HasPrefix(line, "parent ") {
					fields := strings.Fields(line)
					if len(fields) == 2 {
						visit(fields[1])
					}
				}
			}
		}
		if kind == "tree" {
			for i := 0; i < len(data); {
				z := bytes.IndexByte(data[i:], 0)
				if z < 0 || i+z+21 > len(data) {
					break
				}
				off := i + z + 1
				visit(hex.EncodeToString(data[off : off+20]))
				i = off + 20
			}
		}
	}
	for _, id := range roots {
		visit(id)
	}
	return out
}

func (s *objectStore) filterDeepen(ids, boundaries []string, since int64, not map[string]bool) ([]string, []string) {
	blocked:=map[string]bool{}
	if len(not)>0 {
		s.mu.RLock(); refs:=make(map[string]string,len(s.refs)); for k,v:=range s.refs { refs[k]=v }; s.mu.RUnlock()
		for ref,id:=range refs { name:=ref; if i:=strings.LastIndexByte(name,'\x00'); i>=0 { name=name[i+1:] }; if not[name] || not[strings.TrimPrefix(name,"refs/heads/")] { for _,x:=range s.reachable([]string{id}) { blocked[x]=true } } }
	}
	out:=make([]string,0,len(ids)); for _,id:=range ids { if blocked[id] { continue }; kind,data,e:=s.ReadObject(id); if e==nil && kind=="commit" && since>0 { head:=strings.SplitN(string(data),"\n\n",2)[0]; timestamp:=int64(0); for _,line:=range strings.Split(head,"\n") { fields:=strings.Fields(line); if len(fields)>=3 && (fields[0]=="author" || fields[0]=="committer") { timestamp,_=strconv.ParseInt(fields[len(fields)-2],10,64); break } }; if timestamp<since { continue } }; out=append(out,id) }
	b:=boundaries[:0]; for _,id:=range boundaries { if !blocked[id] { b=append(b,id) } }; return out,b
}

// reachableDepth returns commits up to depth generations from each wanted tip,
// along with every tree and blob referenced by those commits. A nonpositive
// depth means full history.
func (s *objectStore) reachableDepth(roots []string, depth int) ([]string, []string) {
	seenObjects, commits, shallow := map[string]bool{}, []string{}, []string{}
	var addTree func(string)
	addTree = func(id string) {
		if seenObjects[id] { return }
		seenObjects[id] = true
		kind, data, err := s.ReadObject(id); if err != nil { return }
		if kind == "tree" {
			for i := 0; i < len(data); { z := bytes.IndexByte(data[i:], 0); if z < 0 || i+z+21 > len(data) { break }; off := i+z+1; addTree(hex.EncodeToString(data[off:off+20])); i=off+20 }
		}
	}
	type node struct{id string; level int}
	queue := []node{}
	for _, id := range roots { queue=append(queue,node{id,1}) }
	seenCommit := map[string]bool{}
	for len(queue)>0 {
		n := queue[0]; queue=queue[1:]; if seenCommit[n.id] { continue }; seenCommit[n.id]=true
		kind,data,err:=s.ReadObject(n.id); if err!=nil || kind!="commit" { continue }
		commits=append(commits,n.id)
		lines:=strings.SplitN(string(data),"\n\n",2)[0]; parents:=[]string{}; tree:=""
		for _, line:=range strings.Split(lines,"\n") { fields:=strings.Fields(line); if len(fields)==2 && fields[0]=="tree" { tree=fields[1] }; if len(fields)==2 && fields[0]=="parent" { parents=append(parents,fields[1]) } }
		if tree!="" { addTree(tree) }
		if depth>0 && n.level>=depth { if len(parents)>0 { shallow=append(shallow,n.id) }; continue }
		for _, p:=range parents { queue=append(queue,node{p,n.level+1}) }
	}
	return append(commits, keys(seenObjects)...), shallow
}

func keys(m map[string]bool) []string { out:=make([]string,0,len(m)); for k:=range m { out=append(out,k) }; return out }

func (s *objectStore) makePack(ids []string) ([]byte, error) {
	var out bytes.Buffer
	out.WriteString("PACK")
	_ = binary.Write(&out, binary.BigEndian, uint32(2))
	_ = binary.Write(&out, binary.BigEndian, uint32(len(ids)))
	for _, id := range ids {
		kind, data, e := s.ReadObject(id)
		if e != nil {
			return nil, e
		}
		typ := packTypeCode[kind]
		if typ == 0 {
			return nil, fmt.Errorf("unsupported type %q", kind)
		}
		n := len(data)
		c := byte(typ<<4) | byte(n&15)
		n >>= 4
		for n > 0 {
			c |= 128
			out.WriteByte(c)
			c = byte(n & 127)
			n >>= 7
		}
		out.WriteByte(c)
		z := zlib.NewWriter(&out)
		if _, e = z.Write(data); e != nil {
			return nil, e
		}
		if e = z.Close(); e != nil {
			return nil, e
		}
	}
	sum := sha1.Sum(out.Bytes())
	out.Write(sum[:])
	return out.Bytes(), nil
}

type packReader struct {
	r *bufio.Reader
	n int
}

func (p *packReader) Read(b []byte) (int, error) { n, e := p.r.Read(b); p.n += n; return n, e }
func (p *packReader) ReadByte() (byte, error) {
	b, e := p.r.ReadByte()
	if e == nil {
		p.n++
	}
	return b, e
}

type packItem struct {
	off     int
	typ     byte
	baseOff int
	baseID  string
	data    []byte
}
type storedObject struct {
	kind string
	data []byte
}

func (s *objectStore) parsePack(raw []byte) error {
	r := &packReader{r: bufio.NewReader(bytes.NewReader(raw))}
	h := make([]byte, 12)
	if _, e := io.ReadFull(r, h); e != nil || string(h[:4]) != "PACK" {
		return fmt.Errorf("invalid pack header")
	}
	v := binary.BigEndian.Uint32(h[4:8])
	if v < 2 || v > 3 {
		return fmt.Errorf("unsupported pack version")
	}
	count := binary.BigEndian.Uint32(h[8:12])
	if count > 100000 {
		return fmt.Errorf("pack too large")
	}
	items := make([]packItem, 0, count)
	for k := uint32(0); k < count; k++ {
		it := packItem{off: r.n}
		c, e := r.ReadByte()
		if e != nil {
			return e
		}
		it.typ = (c >> 4) & 7
		size := int(c & 15)
		shift := uint(4)
		for c&128 != 0 {
			c, e = r.ReadByte()
			if e != nil || shift > 56 {
				return fmt.Errorf("invalid pack size")
			}
			size |= int(c&127) << shift
			shift += 7
		}
		if it.typ == 6 {
			c, e = r.ReadByte()
			if e != nil {
				return e
			}
			dist := int(c & 127)
			for c&128 != 0 {
				c, e = r.ReadByte()
				if e != nil {
					return e
				}
				dist = ((dist + 1) << 7) | int(c&127)
			}
			it.baseOff = it.off - dist
		} else if it.typ == 7 {
			b := make([]byte, 20)
			if _, e = io.ReadFull(r, b); e != nil {
				return e
			}
			it.baseID = hex.EncodeToString(b)
		} else if packCodeType[it.typ] == "" {
			return fmt.Errorf("invalid pack object type")
		}
		z, e := zlib.NewReader(r)
		if e != nil {
			return e
		}
		it.data, e = io.ReadAll(io.LimitReader(z, 64<<20+1))
		z.Close()
		if e != nil || len(it.data) != size {
			return fmt.Errorf("pack object size mismatch")
		}
		items = append(items, it)
	}
	checksum := make([]byte, 20)
	if _, e := io.ReadFull(r, checksum); e != nil {
		return e
	}
	if r.n != len(raw) {
		return fmt.Errorf("trailing pack data")
	}
	sum := sha1.Sum(raw[:len(raw)-20])
	if !bytes.Equal(sum[:], checksum) {
		return fmt.Errorf("pack checksum mismatch")
	}
	resolved := map[int]storedObject{}
	pending := items
	for len(pending) > 0 {
		next := []packItem{}
		progress := false
		for _, it := range pending {
			kind := packCodeType[it.typ]
			data := it.data
			if it.typ == 6 || it.typ == 7 {
				var base storedObject
				var ok bool
				if it.typ == 6 {
					base, ok = resolved[it.baseOff]
				} else {
					k, d, e := s.ReadObject(it.baseID)
					base = storedObject{k, d}
					ok = e == nil
				}
				if !ok {
					next = append(next, it)
					continue
				}
				var e error
				data, e = applyGitDelta(base.data, it.data)
				if e != nil {
					return e
				}
				kind = base.kind
			}
			if _, e := s.WriteObject(kind, data); e != nil {
				return e
			}
			resolved[it.off] = storedObject{kind, data}
			progress = true
		}
		if !progress {
			return fmt.Errorf("unresolved pack delta")
		}
		pending = next
	}
	return nil
}

func applyGitDelta(base, d []byte) ([]byte, error) {
	i := 0
	rv := func() (int, error) {
		n, shift := 0, uint(0)
		for {
			if i >= len(d) || shift > 56 {
				return 0, fmt.Errorf("invalid delta")
			}
			c := d[i]
			i++
			n |= int(c&127) << shift
			shift += 7
			if c&128 == 0 {
				return n, nil
			}
		}
	}
	n, e := rv()
	if e != nil || n != len(base) {
		return nil, fmt.Errorf("delta base size")
	}
	size, e := rv()
	if e != nil || size > 64<<20 {
		return nil, fmt.Errorf("delta target size")
	}
	out := make([]byte, 0, size)
	for i < len(d) {
		op := d[i]
		i++
		if op&128 != 0 {
			off, l := 0, 0
			for j := 0; j < 4; j++ {
				if op&(1<<j) != 0 {
					if i >= len(d) {
						return nil, fmt.Errorf("truncated delta")
					}
					off |= int(d[i]) << (8 * j)
					i++
				}
			}
			for j := 0; j < 3; j++ {
				if op&(1<<(4+j)) != 0 {
					if i >= len(d) {
						return nil, fmt.Errorf("truncated delta")
					}
					l |= int(d[i]) << (8 * j)
					i++
				}
			}
			if l == 0 {
				l = 65536
			}
			if off+l > len(base) {
				return nil, fmt.Errorf("delta bounds")
			}
			out = append(out, base[off:off+l]...)
		} else {
			l := int(op)
			if l == 0 || i+l > len(d) {
				return nil, fmt.Errorf("delta opcode")
			}
			out = append(out, d[i:i+l]...)
			i += l
		}
		if len(out) > size {
			return nil, fmt.Errorf("delta overflow")
		}
	}
	if len(out) != size {
		return nil, fmt.Errorf("delta length")
	}
	return out, nil
}

func (s *objectStore) isAncestor(ancestor, tip string) bool {
	if ancestor == "" || ancestor == "0000000000000000000000000000000000000000" {
		return true
	}
	seen := map[string]bool{}
	queue := []string{tip}
	for len(queue) > 0 {
		id := queue[0]
		queue = queue[1:]
		if id == ancestor {
			return true
		}
		if seen[id] {
			continue
		}
		seen[id] = true
		kind, data, e := s.ReadObject(id)
		if e != nil || kind != "commit" {
			continue
		}
		for _, line := range strings.Split(strings.SplitN(string(data), "\n\n", 2)[0], "\n") {
			if strings.HasPrefix(line, "parent ") {
				queue = append(queue, strings.TrimSpace(strings.TrimPrefix(line, "parent ")))
			}
		}
	}
	return false
}
