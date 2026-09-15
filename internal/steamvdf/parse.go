package steamvdf

import (
	"fmt"
	"strings"
	"unicode"
)

// The general-purpose VDF dependency merges duplicate sections, ignores trailing
// roots and accepts truncated objects. That is useful for tolerant reads, but
// unsafe before replacing an entire configuration file. This bounded parser
// deliberately rejects ambiguous documents instead of normalising away data.
type parser struct {
	s string
	i int
}

func (p *parser) space() {
	for p.i < len(p.s) {
		if strings.HasPrefix(p.s[p.i:], "//") {
			for p.i < len(p.s) && p.s[p.i] != '\n' {
				p.i++
			}
			continue
		}
		if !unicode.IsSpace(rune(p.s[p.i])) {
			return
		}
		p.i++
	}
}
func (p *parser) token() (string, error) {
	p.space()
	if p.i == len(p.s) {
		return "", fmt.Errorf("unexpected end of VDF")
	}
	start := p.i
	if p.s[p.i] == '"' {
		p.i++
		var b strings.Builder
		for p.i < len(p.s) {
			c := p.s[p.i]
			p.i++
			if c == '"' {
				return b.String(), nil
			}
			if c == 0 {
				return "", fmt.Errorf("NUL in VDF")
			}
			if c == '\\' {
				if p.i == len(p.s) {
					break
				}
				c = p.s[p.i]
				p.i++
				switch c {
				case '\\', '"':
				case 'n':
					c = '\n'
				case 'r':
					c = '\r'
				case 't':
					c = '\t'
				default:
					return "", fmt.Errorf("unsupported VDF escape at byte %d", p.i-1)
				}
			}
			b.WriteByte(c)
		}
		return "", fmt.Errorf("unterminated VDF string at byte %d", start)
	}
	for p.i < len(p.s) && !unicode.IsSpace(rune(p.s[p.i])) && !strings.ContainsRune("{}\"", rune(p.s[p.i])) {
		p.i++
	}
	if p.i == start {
		return "", fmt.Errorf("expected VDF string at byte %d", start)
	}
	return p.s[start:p.i], nil
}
func (p *parser) object(depth int) (map[string]any, error) {
	if depth > 128 {
		return nil, fmt.Errorf("VDF nesting exceeds 128")
	}
	m := map[string]any{}
	for {
		p.space()
		if p.i == len(p.s) {
			if depth > 0 {
				return nil, fmt.Errorf("unclosed VDF object")
			}
			return m, nil
		}
		if p.s[p.i] == '}' {
			if depth == 0 {
				return nil, fmt.Errorf("unexpected closing brace")
			}
			p.i++
			return m, nil
		}
		k, e := p.token()
		if e != nil {
			return nil, e
		}
		if _, exists := CaseKey(m, k); exists {
			return nil, fmt.Errorf("duplicate VDF key %q", k)
		}
		p.space()
		if p.i < len(p.s) && p.s[p.i] == '{' {
			p.i++
			v, e := p.object(depth + 1)
			if e != nil {
				return nil, e
			}
			m[k] = v
		} else {
			v, e := p.token()
			if e != nil {
				return nil, e
			}
			m[k] = v
		}
	}
}
func ParseBytes(b []byte) (map[string]any, error) {
	if len(b) > MaxSize {
		return nil, fmt.Errorf("VDF exceeds %d bytes", MaxSize)
	}
	if strings.IndexByte(string(b), 0) >= 0 {
		return nil, fmt.Errorf("NUL in VDF")
	}
	p := parser{s: strings.TrimPrefix(string(b), "\ufeff")}
	return p.object(0)
}
