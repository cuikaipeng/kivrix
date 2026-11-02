package app

import (
	"bytes"
	"encoding/binary"
	"errors"
	"strconv"

	"gopkg.in/yaml.v3"
)

var errDecodeLimit = errors.New("serialized value exceeds the size, nesting or element safety limit")

func decodeYAML(b []byte) (any, error) {
	var root yaml.Node
	if err := yaml.Unmarshal(b, &root); err != nil {
		return nil, err
	}
	// Count aliases before Decode expands them. Cycles and repeated references
	// share a budget, so a small document cannot expand forever.
	items, size := 0, 0
	var check func(*yaml.Node, int) error
	check = func(n *yaml.Node, depth int) error {
		items++
		size += len(n.Value) + 16 + depth*4
		if depth > 64 || items > 100000 || size > maxDecoded {
			return errDecodeLimit
		}
		if n.Alias != nil {
			return check(n.Alias, depth+1)
		}
		for _, child := range n.Content {
			if err := check(child, depth+1); err != nil {
				return err
			}
		}
		return nil
	}
	if err := check(&root, 0); err != nil {
		return nil, err
	}
	var value any
	err := root.Decode(&value)
	return value, err
}

// Check declared lengths before third-party decoders allocate their containers.
func checkSerialized(b []byte, kind string) error {
	if len(b) > maxDecoded {
		return errDecodeLimit
	}
	items := 0
	var value func(int, int) (int, error)
	value = func(i, depth int) (int, error) {
		items++
		if depth > 64 || items > 100000 || i >= len(b) {
			return i, errDecodeLimit
		}
		code := b[i]
		i++
		payload, count, prefix := 0, 0, 0
		if kind == "Msgpack" {
			switch {
			case code <= 0x7f || code >= 0xe0 || code == 0xc0 || code == 0xc2 || code == 0xc3:
			case code >= 0xa0 && code <= 0xbf:
				payload = int(code & 31)
			case code >= 0x90 && code <= 0x9f:
				count = int(code & 15)
			case code >= 0x80 && code <= 0x8f:
				count = int(code&15) * 2
			case code == 0xcc || code == 0xd0:
				payload = 1
			case code == 0xcd || code == 0xd1:
				payload = 2
			case code == 0xca || code == 0xce || code == 0xd2:
				payload = 4
			case code == 0xcb || code == 0xcf || code == 0xd3:
				payload = 8
			case code >= 0xd4 && code <= 0xd8:
				payload = 1 + 1<<uint(code-0xd4)
			case code == 0xc4 || code == 0xc7 || code == 0xd9:
				prefix = 1
			case code == 0xc5 || code == 0xc8 || code == 0xda || code == 0xdc || code == 0xde:
				prefix = 2
			case code == 0xc6 || code == 0xc9 || code == 0xdb || code == 0xdd || code == 0xdf:
				prefix = 4
			default:
				return i, errors.New("invalid MessagePack code")
			}
			if prefix > 0 {
				if prefix > len(b)-i {
					return i, errDecodeLimit
				}
				var n uint32
				for _, v := range b[i : i+prefix] {
					n = n<<8 | uint32(v)
				}
				i += prefix
				if uint64(n) > uint64(len(b)) {
					return i, errDecodeLimit
				}
				if code >= 0xdc {
					count = int(n)
					if code >= 0xde {
						count *= 2
					}
				} else {
					payload = int(n)
					if code >= 0xc7 && code <= 0xc9 {
						payload++
					}
				}
			}
		} else {
			// PHP: bounded scalar tokens, quoted byte strings and nested arrays/objects.
			if code == 'N' {
				if i < len(b) && b[i] == ';' {
					return i + 1, nil
				}
				return i, errDecodeLimit
			}
			if i >= len(b) || b[i] != ':' {
				return i, errDecodeLimit
			}
			i++
			if code == 'b' || code == 'i' || code == 'd' {
				n := bytes.IndexByte(b[i:], ';')
				if n < 0 || n > 256 {
					return i, errDecodeLimit
				}
				return i + n + 1, nil
			}
			number := func() (int, error) {
				end := bytes.IndexByte(b[i:], ':')
				if end < 0 || end > 10 {
					return 0, errDecodeLimit
				}
				n, err := strconv.Atoi(string(b[i : i+end]))
				i += end + 1
				if err != nil || n < 0 || n > len(b)-i {
					return 0, errDecodeLimit
				}
				return n, nil
			}
			n, err := number()
			if err != nil {
				return i, err
			}
			if code == 's' || code == 'O' {
				if n+3 > len(b)-i || b[i] != '"' || b[i+n+1] != '"' {
					return i, errDecodeLimit
				}
				i += n + 2
				if code == 's' {
					if b[i] != ';' {
						return i, errDecodeLimit
					}
					return i + 1, nil
				}
				if b[i] != ':' {
					return i, errDecodeLimit
				}
				i++
				n, err = number()
				if err != nil {
					return i, err
				}
			} else if code != 'a' {
				return i, errDecodeLimit
			}
			if i >= len(b) || b[i] != '{' {
				return i, errDecodeLimit
			}
			i++
			count = n * 2
		}
		if payload > len(b)-i || count > 100000-items {
			return i, errDecodeLimit
		}
		i += payload
		for n := 0; n < count; n++ {
			var err error
			i, err = value(i, depth+1)
			if err != nil {
				return i, err
			}
		}
		if kind == "PHP" {
			if i >= len(b) || b[i] != '}' {
				return i, errDecodeLimit
			}
			i++
		}
		return i, nil
	}
	_, err := value(0, 0)
	return err
}

// Pickle frames and strings carry attacker-controlled allocation sizes. Scan
// instructions without constructing objects; the decoder still validates syntax.
func checkPickle(b []byte) error {
	if len(b) > maxDecoded {
		return errDecodeLimit
	}
	for i, ops := 0, 0; i < len(b); ops++ {
		if ops > 100000 {
			return errDecodeLimit
		}
		code := b[i]
		i++
		fixed, prefix, lines := 0, 0, 0
		switch code {
		case '.':
			return nil
		case 'I', 'L', 'S', 'V', 'F', 'g', 'p', 'P':
			lines = 1
		case 'c', 'i':
			lines = 2
		case 'J', 'j', 'r', 0x84:
			fixed = 4
		case 'K', 'h', 'q', 0x80, 0x82:
			fixed = 1
		case 'M', 0x83:
			fixed = 2
		case 'G':
			fixed = 8
		case 'U', 'C', 0x8a, 0x8c:
			prefix = 1
		case 'T', 'B', 'X', 0x8b:
			prefix = 4
		case 0x8d, 0x8e, 0x95, 0x96:
			prefix = 8
		case 'N', ']', 'a', 'e', 'l', ')', 't', '}', 'd', 's', 'u', '0', '2', '(', '1', 'R', 'b', 'o', 'Q',
			0x81, 0x85, 0x86, 0x87, 0x88, 0x89, 0x8f, 0x90, 0x91, 0x92, 0x93, 0x94, 0x97, 0x98:
		default:
			return errors.New("invalid Pickle opcode")
		}
		for n := 0; n < lines; n++ {
			end := bytes.IndexByte(b[i:], '\n')
			if end < 0 {
				return errDecodeLimit
			}
			i += end + 1
		}
		if prefix > 0 {
			if prefix > len(b)-i {
				return errDecodeLimit
			}
			var size [8]byte
			copy(size[:], b[i:i+prefix])
			n := binary.LittleEndian.Uint64(size[:])
			i += prefix
			if n > uint64(len(b)-i) {
				return errDecodeLimit
			}
			if code != 0x95 {
				fixed = int(n)
			}
		}
		if fixed > len(b)-i {
			return errDecodeLimit
		}
		i += fixed
	}
	return errors.New("Pickle has no STOP opcode")
}

func checkJSONDepth(b []byte) error {
	depth, quoted, escape := 0, false, false
	for _, c := range b {
		if quoted {
			if escape {
				escape = false
			} else if c == '\\' {
				escape = true
			} else if c == '"' {
				quoted = false
			}
			continue
		}
		switch c {
		case '"':
			quoted = true
		case '{', '[':
			depth++
			if depth > 64 {
				return errDecodeLimit
			}
		case '}', ']':
			depth--
		}
	}
	return nil
}
