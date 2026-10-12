package jsonx

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"unicode/utf8"
)

func Check(b []byte) error {
	if !utf8.Valid(b) {
		return fmt.Errorf("invalid UTF-8")
	}
	d := json.NewDecoder(bytes.NewReader(b))
	d.UseNumber()
	if e := walk(d, 0); e != nil {
		return e
	}
	if _, e := d.Token(); e != io.EOF {
		return fmt.Errorf("trailing JSON")
	}
	return nil
}
func walk(d *json.Decoder, depth int) error {
	if depth > 32 {
		return fmt.Errorf("JSON depth")
	}
	t, e := d.Token()
	if e != nil {
		return e
	}
	delim, ok := t.(json.Delim)
	if !ok {
		return nil
	}
	switch delim {
	case '{':
		seen := map[string]bool{}
		for d.More() {
			k, e := d.Token()
			if e != nil {
				return e
			}
			s, ok := k.(string)
			if !ok || seen[s] {
				return fmt.Errorf("duplicate JSON field")
			}
			seen[s] = true
			if e = walk(d, depth+1); e != nil {
				return e
			}
		}
		end, e := d.Token()
		if e != nil || end != json.Delim('}') {
			return fmt.Errorf("JSON object")
		}
	case '[':
		for d.More() {
			if e := walk(d, depth+1); e != nil {
				return e
			}
		}
		end, e := d.Token()
		if e != nil || end != json.Delim(']') {
			return fmt.Errorf("JSON array")
		}
	default:
		return fmt.Errorf("JSON delimiter")
	}
	return nil
}
func Decode(b []byte, v any) error {
	if t := bytes.TrimSpace(b); len(t) == 0 || t[0] != '{' {
		return fmt.Errorf("JSON object required")
	}
	if e := Check(b); e != nil {
		return e
	}
	d := json.NewDecoder(bytes.NewReader(b))
	d.DisallowUnknownFields()
	return d.Decode(v)
}
