package contract

import (
	"bytes"
	"embed"
	"encoding/json"
	"fmt"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"
)

//go:embed openapi.json
var files embed.FS

type Schema struct {
	Ref        string            `json:"$ref"`
	Type       string            `json:"type"`
	Nullable   bool              `json:"nullable"`
	Properties map[string]Schema `json:"properties"`
	Required   []string          `json:"required"`
	Additional any               `json:"additionalProperties"`
	Items      *Schema           `json:"items"`
	Enum       []any             `json:"enum"`
	Pattern    string            `json:"pattern"`
	MinLength  *int              `json:"minLength"`
	MaxLength  *int              `json:"maxLength"`
	MaxItems   *int              `json:"maxItems"`
	Minimum    *float64          `json:"minimum"`
	Maximum    *float64          `json:"maximum"`
	AllOf      []Schema          `json:"allOf"`
}
type Content struct {
	Schema Schema `json:"schema"`
}
type Operation struct {
	Parameters []struct {
		Name     string `json:"name"`
		In       string `json:"in"`
		Required bool   `json:"required"`
		Schema   Schema `json:"schema"`
	} `json:"parameters"`
	Request *struct {
		Content map[string]Content `json:"content"`
	} `json:"requestBody"`
	Responses map[string]struct {
		Content map[string]Content `json:"content"`
	} `json:"responses"`
}
type Document struct {
	Paths      map[string]map[string]Operation `json:"paths"`
	Components struct {
		Schemas map[string]Schema `json:"schemas"`
	} `json:"components"`
}

var Doc = func() Document {
	b, e := files.ReadFile("openapi.json")
	if e != nil {
		panic(e)
	}
	var d Document
	if e = json.Unmarshal(b, &d); e != nil {
		panic(e)
	}
	return d
}()

func ValidateJSON(b []byte, s Schema) error {
	var v any
	d := json.NewDecoder(bytes.NewReader(b))
	d.UseNumber()
	if e := d.Decode(&v); e != nil {
		return e
	}
	return validate(v, s, "root", 0)
}
func validate(v any, s Schema, path string, depth int) error {
	if depth > 64 {
		return fmt.Errorf("schema depth")
	}
	if v == nil {
		if s.Nullable {
			return nil
		}
		return fmt.Errorf("%s null", path)
	}
	if s.Ref != "" {
		r, ok := Doc.Components.Schemas[strings.TrimPrefix(s.Ref, "#/components/schemas/")]
		if !ok {
			return fmt.Errorf("unresolved ref")
		}
		return validate(v, r, path, depth+1)
	}
	for _, part := range s.AllOf {
		if e := validate(v, part, path, depth+1); e != nil {
			return e
		}
	}
	if len(s.Enum) > 0 {
		found := false
		for _, x := range s.Enum {
			if fmt.Sprint(x) == fmt.Sprint(v) {
				found = true
			}
		}
		if !found {
			return fmt.Errorf("%s enum", path)
		}
	}
	switch s.Type {
	case "object":
		m, ok := v.(map[string]any)
		if !ok {
			return fmt.Errorf("%s object", path)
		}
		for _, key := range s.Required {
			if _, ok = m[key]; !ok {
				return fmt.Errorf("%s required %s", path, key)
			}
		}
		for key, x := range m {
			p, ok := s.Properties[key]
			if !ok {
				if b, ok := s.Additional.(bool); ok && !b {
					return fmt.Errorf("%s unknown %s", path, key)
				}
				continue
			}
			if e := validate(x, p, path+"."+key, depth+1); e != nil {
				return e
			}
		}
	case "array":
		a, ok := v.([]any)
		if !ok {
			return fmt.Errorf("%s array", path)
		}
		if s.MaxItems != nil && len(a) > *s.MaxItems {
			return fmt.Errorf("%s maxItems", path)
		}
		if s.Items != nil {
			for i, x := range a {
				if e := validate(x, *s.Items, fmt.Sprintf("%s[%d]", path, i), depth+1); e != nil {
					return e
				}
			}
		}
	case "string":
		x, ok := v.(string)
		if !ok {
			return fmt.Errorf("%s string", path)
		}
		n := utf8.RuneCountInString(x)
		if s.MinLength != nil && n < *s.MinLength || s.MaxLength != nil && n > *s.MaxLength {
			return fmt.Errorf("%s length", path)
		}
		if s.Pattern != "" {
			matched, e := regexp.MatchString(s.Pattern, x)
			if e != nil || !matched {
				return fmt.Errorf("%s pattern", path)
			}
		}
	case "integer", "number":
		x, ok := v.(json.Number)
		if !ok {
			return fmt.Errorf("%s number", path)
		}
		if s.Type == "integer" {
			if _, e := x.Int64(); e != nil {
				return fmt.Errorf("%s integer", path)
			}
		}
		f, e := strconv.ParseFloat(string(x), 64)
		if e != nil || s.Minimum != nil && f < *s.Minimum || s.Maximum != nil && f > *s.Maximum {
			return fmt.Errorf("%s bound", path)
		}
	case "boolean":
		if _, ok := v.(bool); !ok {
			return fmt.Errorf("%s boolean", path)
		}
	}
	return nil
}
func ValidateRequest(method, path string, b []byte) error {
	op, ok := Doc.Paths[path][strings.ToLower(method)]
	if !ok || op.Request == nil {
		return fmt.Errorf("request contract not found")
	}
	return ValidateJSON(b, op.Request.Content["application/json"].Schema)
}
func ValidateQuery(method, path string, q url.Values) error {
	op, ok := Doc.Paths[path][strings.ToLower(method)]
	if !ok {
		return fmt.Errorf("operation not found")
	}
	allowed := map[string]Schema{}
	required := map[string]bool{}
	for _, p := range op.Parameters {
		if p.In == "query" {
			allowed[p.Name] = p.Schema
			required[p.Name] = p.Required
		}
	}
	values := map[string]any{}
	for k, v := range q {
		s, ok := allowed[k]
		if !ok || len(v) != 1 || !utf8.ValidString(v[0]) {
			return fmt.Errorf("query parameter")
		}
		if v[0] == "" && !required[k] {
			continue
		}
		switch s.Type {
		case "integer", "number":
			values[k] = json.Number(v[0])
		case "boolean":
			if v[0] != "true" && v[0] != "false" {
				return fmt.Errorf("boolean query")
			}
			values[k] = v[0] == "true"
		default:
			values[k] = v[0]
		}
	}
	for k, yes := range required {
		if yes {
			if _, ok := values[k]; !ok {
				return fmt.Errorf("required query")
			}
		}
	}
	for k, v := range values {
		if e := validate(v, allowed[k], k, 0); e != nil {
			return e
		}
	}
	return nil
}
func ValidateResponse(method, path string, status int, b []byte) error {
	op, ok := Doc.Paths[path][strings.ToLower(method)]
	if !ok {
		return fmt.Errorf("operation not found")
	}
	response, ok := op.Responses[strconv.Itoa(status)]
	if !ok {
		return fmt.Errorf("status not declared")
	}
	if status == 204 {
		if len(b) != 0 {
			return fmt.Errorf("204 has body")
		}
		return nil
	}
	schema, ok := response.Content["application/json"]
	if !ok {
		return fmt.Errorf("json response not declared")
	}
	return ValidateJSON(b, schema.Schema)
}
