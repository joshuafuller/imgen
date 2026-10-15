// Package pngmeta embeds and reads text metadata in PNG and JPEG images.
package pngmeta

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"hash/crc32"
	"sort"
	"strings"
)

var (
	pngSig  = []byte("\x89PNG\r\n\x1a\n")
	errPNG  = errors.New("pngmeta: not a valid PNG")
	errJPEG = errors.New("pngmeta: not a valid JPEG")
)

// Embed writes key/value text metadata into an image: PNG iTXt chunks before
// IEND; JPEG a COM segment after SOI (JSON). Other formats return data unchanged.
func Embed(data []byte, mime string, kv map[string]string) ([]byte, error) {
	switch mime {
	case "image/png":
		return embedPNG(data, kv)
	case "image/jpeg":
		return embedJPEG(data, kv)
	}
	return data, nil
}

// Read parses metadata written by Embed. Unsupported formats yield an empty map.
func Read(data []byte, mime string) (map[string]string, error) {
	switch mime {
	case "image/png":
		return readPNG(data)
	case "image/jpeg":
		return readJPEG(data)
	}
	return map[string]string{}, nil
}

func pngChunk(typ string, body []byte) []byte {
	out := make([]byte, 0, 12+len(body))
	out = binary.BigEndian.AppendUint32(out, uint32(len(body))) // #nosec G115 -- iTXt bodies are far below 4 GiB
	out = append(out, typ...)
	out = append(out, body...)
	crc := crc32.ChecksumIEEE(out[4:])
	return binary.BigEndian.AppendUint32(out, crc)
}

func embedPNG(data []byte, kv map[string]string) ([]byte, error) {
	if !bytes.HasPrefix(data, pngSig) {
		return nil, errPNG
	}
	if len(kv) == 0 {
		return data, nil
	}
	// Locate IEND by walking chunks (a raw search could hit IDAT bytes).
	pos := len(pngSig)
	iend := -1
	for pos+12 <= len(data) {
		n := int(binary.BigEndian.Uint32(data[pos:]))
		if string(data[pos+4:pos+8]) == "IEND" {
			iend = pos
			break
		}
		pos += 12 + n
	}
	if iend < 0 {
		return nil, errPNG
	}
	keys := make([]string, 0, len(kv))
	for k := range kv {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := make([]byte, 0, len(data)+64*len(kv))
	out = append(out, data[:iend]...)
	for _, k := range keys {
		if k == "" || len(k) > 79 || strings.ContainsRune(k, 0) {
			return nil, fmt.Errorf("pngmeta: invalid key %q", k)
		}
		body := append([]byte(k), 0, 0, 0, 0, 0) // key NUL, uncompressed, method, empty lang NUL, empty translated NUL
		body = append(body, kv[k]...)
		out = append(out, pngChunk("iTXt", body)...)
	}
	return append(out, data[iend:]...), nil
}

func readPNG(data []byte) (map[string]string, error) {
	if !bytes.HasPrefix(data, pngSig) {
		return nil, errPNG
	}
	res := map[string]string{}
	pos := len(pngSig)
	for pos+12 <= len(data) {
		n := int(binary.BigEndian.Uint32(data[pos:]))
		typ := string(data[pos+4 : pos+8])
		end := pos + 8 + n
		if end+4 > len(data) {
			return nil, errPNG
		}
		if typ == "iTXt" {
			body := data[pos+8 : end]
			if k, v, ok := parseITXt(body); ok {
				res[k] = v
			}
		}
		if typ == "IEND" {
			break
		}
		pos = end + 4
	}
	return res, nil
}

func parseITXt(b []byte) (key, val string, ok bool) {
	i := bytes.IndexByte(b, 0)
	if i < 0 || len(b) < i+3 || b[i+1] != 0 { // compressed chunks are not ours
		return "", "", false
	}
	rest := b[i+3:]
	j := bytes.IndexByte(rest, 0) // language tag
	if j < 0 {
		return "", "", false
	}
	rest = rest[j+1:]
	k := bytes.IndexByte(rest, 0) // translated keyword
	if k < 0 {
		return "", "", false
	}
	return string(b[:i]), string(rest[k+1:]), true
}

func embedJPEG(data []byte, kv map[string]string) ([]byte, error) {
	if len(data) < 2 || data[0] != 0xFF || data[1] != 0xD8 {
		return nil, errJPEG
	}
	if len(kv) == 0 {
		return data, nil
	}
	js, err := json.Marshal(kv)
	if err != nil {
		return nil, err
	}
	if len(js)+2 > 0xFFFF {
		return nil, errors.New("pngmeta: metadata too large for JPEG COM segment")
	}
	out := make([]byte, 0, len(data)+len(js)+4)
	out = append(out, 0xFF, 0xD8, 0xFF, 0xFE)
	out = binary.BigEndian.AppendUint16(out, uint16(len(js)+2)) // #nosec G115 -- length checked above
	out = append(out, js...)
	return append(out, data[2:]...), nil
}

func readJPEG(data []byte) (map[string]string, error) {
	if len(data) < 2 || data[0] != 0xFF || data[1] != 0xD8 {
		return nil, errJPEG
	}
	res := map[string]string{}
	pos := 2
	for pos+4 <= len(data) && data[pos] == 0xFF {
		m := data[pos+1]
		if m == 0xDA || m == 0xD9 { // start of scan / EOI: headers are over
			break
		}
		n := int(binary.BigEndian.Uint16(data[pos+2:]))
		if n < 2 || pos+2+n > len(data) {
			return nil, errJPEG
		}
		if m == 0xFE {
			var kv map[string]string
			if json.Unmarshal(data[pos+4:pos+2+n], &kv) == nil {
				for k, v := range kv {
					res[k] = v
				}
			}
		}
		pos += 2 + n
	}
	return res, nil
}
