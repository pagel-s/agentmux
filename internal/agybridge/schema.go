package agybridge

import (
	"encoding/json"
	"errors"
	"math"
	"regexp"
	"strings"
)

var functionName = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_.-]{0,63}$`)

func validateTools(tools []Tool) error {
	seen := map[string]bool{}
	for _, tool := range tools {
		if tool.Type != "function" || !functionName.MatchString(tool.Function.Name) || seen[tool.Function.Name] || len(tool.Function.Parameters) > 64*1024 {
			return errors.New("invalid tool declaration")
		}
		seen[tool.Function.Name] = true
		var schema map[string]any
		if json.Unmarshal(tool.Function.Parameters, &schema) != nil || schema == nil || schema["type"] != "object" {
			return errors.New("tool args must have an object schema")
		}
		if err := inspectSchema(schema, 0); err != nil {
			return err
		}
	}
	return nil
}

func inspectSchema(schema map[string]any, depth int) error {
	if depth > 16 || len(schema) > 32 {
		return errors.New("schema complexity limit")
	}
	for key, value := range schema {
		switch key {
		case "$schema", "title", "description", "default", "examples":
		case "type":
			switch kind := value.(type) {
			case string:
				if !validType(kind) {
					return errors.New("unsupported type")
				}
			case []any:
				if len(kind) > 7 {
					return errors.New("too many types")
				}
				for _, entry := range kind {
					name, ok := entry.(string)
					if !ok || !validType(name) {
						return errors.New("unsupported type")
					}
				}
			default:
				return errors.New("invalid type")
			}
		case "properties":
			properties, ok := value.(map[string]any)
			if !ok || len(properties) > 64 {
				return errors.New("invalid properties")
			}
			for _, child := range properties {
				nested, ok := child.(map[string]any)
				if !ok {
					return errors.New("invalid property schema")
				}
				if err := inspectSchema(nested, depth+1); err != nil {
					return err
				}
			}
		case "required":
			required, ok := value.([]any)
			if !ok || len(required) > 64 {
				return errors.New("invalid required")
			}
			for _, entry := range required {
				if _, ok := entry.(string); !ok {
					return errors.New("invalid required name")
				}
			}
		case "additionalProperties":
			switch nested := value.(type) {
			case bool:
			case map[string]any:
				if err := inspectSchema(nested, depth+1); err != nil {
					return err
				}
			default:
				return errors.New("invalid additional properties")
			}
		case "items":
			nested, ok := value.(map[string]any)
			if !ok {
				return errors.New("unsupported array schema")
			}
			if err := inspectSchema(nested, depth+1); err != nil {
				return err
			}
		case "anyOf", "oneOf", "allOf":
			choices, ok := value.([]any)
			if !ok || len(choices) < 1 || len(choices) > 8 {
				return errors.New("invalid schema union")
			}
			for _, choice := range choices {
				nested, ok := choice.(map[string]any)
				if !ok {
					return errors.New("invalid union branch")
				}
				if err := inspectSchema(nested, depth+1); err != nil {
					return err
				}
			}
		case "enum":
			values, ok := value.([]any)
			if !ok || len(values) < 1 || len(values) > 64 {
				return errors.New("invalid enum")
			}
		case "const":
		case "minimum", "maximum", "exclusiveMinimum", "exclusiveMaximum", "multipleOf", "minLength", "maxLength", "minItems", "maxItems", "minProperties", "maxProperties":
			number, ok := value.(float64)
			if !ok || math.IsNaN(number) || math.IsInf(number, 0) {
				return errors.New("invalid numeric schema bound")
			}
			if (strings.HasPrefix(key, "min") || strings.HasPrefix(key, "max")) && key != "minimum" && key != "maximum" && (number < 0 || math.Trunc(number) != number) {
				return errors.New("invalid size bound")
			}
			if key == "multipleOf" && number <= 0 {
				return errors.New("invalid multiple")
			}
		case "pattern":
			pattern, ok := value.(string)
			if !ok || len(pattern) > 4096 {
				return errors.New("invalid pattern")
			}
			if _, err := regexp.Compile(pattern); err != nil {
				return errors.New("unsupported pattern")
			}
		case "uniqueItems":
			if _, ok := value.(bool); !ok {
				return errors.New("invalid uniqueItems")
			}
		default:
			return errors.New("unsupported schema keyword")
		}
	}
	return nil
}

func validType(kind string) bool {
	switch kind {
	case "object", "array", "string", "number", "integer", "boolean", "null":
		return true
	}
	return false
}
func matchesType(value any, kind string) bool {
	switch kind {
	case "object":
		_, ok := value.(map[string]any)
		return ok
	case "array":
		_, ok := value.([]any)
		return ok
	case "string":
		_, ok := value.(string)
		return ok
	case "number":
		_, ok := value.(float64)
		return ok
	case "integer":
		number, ok := value.(float64)
		return ok && math.Trunc(number) == number
	case "boolean":
		_, ok := value.(bool)
		return ok
	case "null":
		return value == nil
	}
	return false
}
func equalJSON(left, right any) bool {
	l, _ := json.Marshal(left)
	r, _ := json.Marshal(right)
	return string(l) == string(r)
}

func validateValue(value any, schema map[string]any, depth int) error {
	invalid := errors.New("tool arguments do not satisfy declared schema")
	if depth > 16 {
		return invalid
	}
	if kind, ok := schema["type"].(string); ok && !matchesType(value, kind) {
		return invalid
	}
	if kinds, ok := schema["type"].([]any); ok {
		matched := false
		for _, kind := range kinds {
			if matchesType(value, kind.(string)) {
				matched = true
			}
		}
		if !matched {
			return invalid
		}
	}
	if choices, ok := schema["enum"].([]any); ok {
		matched := false
		for _, choice := range choices {
			if equalJSON(value, choice) {
				matched = true
			}
		}
		if !matched {
			return invalid
		}
	}
	if constant, ok := schema["const"]; ok && !equalJSON(value, constant) {
		return invalid
	}
	for _, key := range []string{"anyOf", "oneOf", "allOf"} {
		if choices, ok := schema[key].([]any); ok {
			matches := 0
			for _, choice := range choices {
				if validateValue(value, choice.(map[string]any), depth+1) == nil {
					matches++
				}
			}
			if (key == "anyOf" && matches == 0) || (key == "oneOf" && matches != 1) || (key == "allOf" && matches != len(choices)) {
				return invalid
			}
		}
	}
	bound := func(key string, size float64, minimum bool) bool {
		if limit, ok := schema[key].(float64); ok {
			if minimum {
				return size >= limit
			}
			return size <= limit
		}
		return true
	}
	switch v := value.(type) {
	case map[string]any:
		if len(v) > 256 || !bound("minProperties", float64(len(v)), true) || !bound("maxProperties", float64(len(v)), false) {
			return invalid
		}
		properties, _ := schema["properties"].(map[string]any)
		if required, ok := schema["required"].([]any); ok {
			for _, name := range required {
				if _, ok := v[name.(string)]; !ok {
					return invalid
				}
			}
		}
		for name, entry := range v {
			if property, ok := properties[name].(map[string]any); ok {
				if err := validateValue(entry, property, depth+1); err != nil {
					return err
				}
				continue
			}
			switch extra := schema["additionalProperties"].(type) {
			case bool:
				if !extra {
					return invalid
				}
			case map[string]any:
				if err := validateValue(entry, extra, depth+1); err != nil {
					return err
				}
			}
		}
	case []any:
		if len(v) > 1024 || !bound("minItems", float64(len(v)), true) || !bound("maxItems", float64(len(v)), false) {
			return invalid
		}
		for i, entry := range v {
			if items, ok := schema["items"].(map[string]any); ok {
				if err := validateValue(entry, items, depth+1); err != nil {
					return err
				}
			}
			if schema["uniqueItems"] == true {
				for j := 0; j < i; j++ {
					if equalJSON(v[j], entry) {
						return invalid
					}
				}
			}
		}
	case string:
		if !bound("minLength", float64(len([]rune(v))), true) || !bound("maxLength", float64(len([]rune(v))), false) {
			return invalid
		}
		if pattern, ok := schema["pattern"].(string); ok {
			compiled, _ := regexp.Compile(pattern)
			if !compiled.MatchString(v) {
				return invalid
			}
		}
	case float64:
		if !bound("minimum", v, true) || !bound("maximum", v, false) {
			return invalid
		}
		if limit, ok := schema["exclusiveMinimum"].(float64); ok && v <= limit {
			return invalid
		}
		if limit, ok := schema["exclusiveMaximum"].(float64); ok && v >= limit {
			return invalid
		}
		if multiple, ok := schema["multipleOf"].(float64); ok && math.Abs(v/multiple-math.Round(v/multiple)) > 1e-9 {
			return invalid
		}
	}
	return nil
}
