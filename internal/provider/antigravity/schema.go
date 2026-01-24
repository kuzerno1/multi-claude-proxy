package antigravity

import (
	"fmt"
	"strings"
)

// SanitizeSchema cleans JSON Schema for Antigravity API compatibility.
// Uses allowlist approach - only permits known-safe JSON Schema features.
func SanitizeSchema(schema map[string]interface{}) map[string]interface{} {
	if schema == nil {
		return createPlaceholderSchema()
	}

	allowedFields := map[string]bool{
		"type":        true,
		"description": true,
		"properties":  true,
		"required":    true,
		"items":       true,
		"enum":        true,
		"title":       true,
	}

	sanitized := make(map[string]interface{})

	for key, value := range schema {
		// Convert "const" to "enum" for compatibility
		if key == "const" {
			sanitized["enum"] = []interface{}{value}
			continue
		}

		if !allowedFields[key] {
			continue
		}

		switch key {
		case "properties":
			if props, ok := value.(map[string]interface{}); ok {
				newProps := make(map[string]interface{})
				for propKey, propVal := range props {
					if propMap, ok := propVal.(map[string]interface{}); ok {
						newProps[propKey] = SanitizeSchema(propMap)
					}
				}
				sanitized["properties"] = newProps
			}
		case "items":
			if itemMap, ok := value.(map[string]interface{}); ok {
				sanitized["items"] = SanitizeSchema(itemMap)
			} else if itemArr, ok := value.([]interface{}); ok {
				newItems := make([]interface{}, len(itemArr))
				for i, item := range itemArr {
					if itemMap, ok := item.(map[string]interface{}); ok {
						newItems[i] = SanitizeSchema(itemMap)
					} else {
						newItems[i] = item
					}
				}
				sanitized["items"] = newItems
			} else {
				sanitized["items"] = value
			}
		default:
			sanitized[key] = value
		}
	}

	// Ensure we have at least a type
	if _, ok := sanitized["type"]; !ok {
		sanitized["type"] = "object"
	}

	// If array type without items, add default items definition
	if sanitized["type"] == "array" {
		if _, hasItems := sanitized["items"]; !hasItems {
			sanitized["items"] = map[string]interface{}{"type": "string"}
		}
	}

	// If object type with no properties, add placeholder
	if sanitized["type"] == "object" {
		props, hasProps := sanitized["properties"].(map[string]interface{})
		if !hasProps || len(props) == 0 {
			sanitized["properties"] = map[string]interface{}{
				"reason": map[string]interface{}{
					"type":        "string",
					"description": "Reason for calling this tool",
				},
			}
			sanitized["required"] = []interface{}{"reason"}
		}
	}

	return sanitized
}

// CleanSchema applies the full cleaning pipeline for Gemini API compatibility.
func CleanSchema(schema map[string]interface{}) map[string]interface{} {
	if schema == nil {
		return schema
	}

	// Phase 1: Convert $refs to hints
	result := convertRefsToHints(schema)

	// Phase 1b: Add enum hints
	result = addEnumHints(result)

	// Phase 1c: Add additionalProperties hints
	result = addAdditionalPropertiesHints(result)

	// Phase 1d: Move constraints to description
	result = moveConstraintsToDescription(result)

	// Phase 2a: Merge allOf schemas
	result = mergeAllOf(result)

	// Phase 2b: Flatten anyOf/oneOf
	result = flattenAnyOfOneOf(result)

	// Phase 2c: Flatten type arrays
	result = flattenTypeArrays(result)

	// Phase 3: Remove unsupported keywords
	unsupported := []string{
		"additionalProperties", "default", "$schema", "$defs",
		"definitions", "$ref", "$id", "$comment", "title",
		"minLength", "maxLength", "pattern", "format",
		"minItems", "maxItems", "examples", "allOf", "anyOf", "oneOf",
	}
	for _, key := range unsupported {
		delete(result, key)
	}

	// Phase 4: Recursively clean nested schemas
	if props, ok := result["properties"].(map[string]interface{}); ok {
		newProps := make(map[string]interface{})
		for key, value := range props {
			if propMap, ok := value.(map[string]interface{}); ok {
				newProps[key] = CleanSchema(propMap)
			} else {
				newProps[key] = value
			}
		}
		result["properties"] = newProps
	}

	if items, ok := result["items"].(map[string]interface{}); ok {
		result["items"] = CleanSchema(items)
	}

	// If array type without items, add default items definition
	if typeVal, ok := result["type"].(string); ok && (typeVal == "array" || typeVal == "ARRAY") {
		if _, hasItems := result["items"]; !hasItems {
			result["items"] = map[string]interface{}{"type": "STRING"}
		}
	}

	// Validate required array
	if required, ok := result["required"].([]interface{}); ok {
		if props, ok := result["properties"].(map[string]interface{}); ok {
			validRequired := make([]interface{}, 0)
			for _, req := range required {
				if reqStr, ok := req.(string); ok {
					if _, exists := props[reqStr]; exists {
						validRequired = append(validRequired, req)
					}
				}
			}
			if len(validRequired) > 0 {
				result["required"] = validRequired
			} else {
				delete(result, "required")
			}
		}
	}

	// Phase 5: Convert type to Google's uppercase format
	if typeVal, ok := result["type"].(string); ok {
		result["type"] = toGoogleType(typeVal)
	}

	return result
}

func createPlaceholderSchema() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"reason": map[string]interface{}{
				"type":        "string",
				"description": "Reason for calling this tool",
			},
		},
		"required": []interface{}{"reason"},
	}
}

func appendDescriptionHint(schema map[string]interface{}, hint string) map[string]interface{} {
	if schema == nil {
		return schema
	}
	result := copyMap(schema)
	if desc, ok := result["description"].(string); ok && desc != "" {
		result["description"] = fmt.Sprintf("%s (%s)", desc, hint)
	} else {
		result["description"] = hint
	}
	return result
}

func convertRefsToHints(schema map[string]interface{}) map[string]interface{} {
	if schema == nil {
		return schema
	}
	result := copyMap(schema)

	if ref, ok := result["$ref"].(string); ok {
		parts := strings.Split(ref, "/")
		defName := parts[len(parts)-1]
		hint := fmt.Sprintf("See: %s", defName)
		desc := ""
		if existingDesc, ok := result["description"].(string); ok {
			desc = fmt.Sprintf("%s (%s)", existingDesc, hint)
		} else {
			desc = hint
		}
		return map[string]interface{}{"type": "object", "description": desc}
	}

	if props, ok := result["properties"].(map[string]interface{}); ok {
		newProps := make(map[string]interface{})
		for key, value := range props {
			if propMap, ok := value.(map[string]interface{}); ok {
				newProps[key] = convertRefsToHints(propMap)
			} else {
				newProps[key] = value
			}
		}
		result["properties"] = newProps
	}

	if items, ok := result["items"].(map[string]interface{}); ok {
		result["items"] = convertRefsToHints(items)
	}

	for _, key := range []string{"anyOf", "oneOf", "allOf"} {
		if arr, ok := result[key].([]interface{}); ok {
			newArr := make([]interface{}, len(arr))
			for i, item := range arr {
				if itemMap, ok := item.(map[string]interface{}); ok {
					newArr[i] = convertRefsToHints(itemMap)
				} else {
					newArr[i] = item
				}
			}
			result[key] = newArr
		}
	}

	return result
}

func addEnumHints(schema map[string]interface{}) map[string]interface{} {
	if schema == nil {
		return schema
	}
	result := copyMap(schema)

	if enum, ok := result["enum"].([]interface{}); ok && len(enum) > 1 && len(enum) <= 10 {
		vals := make([]string, len(enum))
		for i, v := range enum {
			vals[i] = fmt.Sprintf("%v", v)
		}
		result = appendDescriptionHint(result, fmt.Sprintf("Allowed: %s", strings.Join(vals, ", ")))
	}

	if props, ok := result["properties"].(map[string]interface{}); ok {
		newProps := make(map[string]interface{})
		for key, value := range props {
			if propMap, ok := value.(map[string]interface{}); ok {
				newProps[key] = addEnumHints(propMap)
			} else {
				newProps[key] = value
			}
		}
		result["properties"] = newProps
	}

	if items, ok := result["items"].(map[string]interface{}); ok {
		result["items"] = addEnumHints(items)
	}

	return result
}

func addAdditionalPropertiesHints(schema map[string]interface{}) map[string]interface{} {
	if schema == nil {
		return schema
	}
	result := copyMap(schema)

	if ap, ok := result["additionalProperties"].(bool); ok && !ap {
		result = appendDescriptionHint(result, "No extra properties allowed")
	}

	if props, ok := result["properties"].(map[string]interface{}); ok {
		newProps := make(map[string]interface{})
		for key, value := range props {
			if propMap, ok := value.(map[string]interface{}); ok {
				newProps[key] = addAdditionalPropertiesHints(propMap)
			} else {
				newProps[key] = value
			}
		}
		result["properties"] = newProps
	}

	if items, ok := result["items"].(map[string]interface{}); ok {
		result["items"] = addAdditionalPropertiesHints(items)
	}

	return result
}

func moveConstraintsToDescription(schema map[string]interface{}) map[string]interface{} {
	if schema == nil {
		return schema
	}
	result := copyMap(schema)

	constraints := []string{"minLength", "maxLength", "pattern", "minimum", "maximum",
		"minItems", "maxItems", "format"}

	for _, constraint := range constraints {
		if val, ok := result[constraint]; ok {
			if _, isMap := val.(map[string]interface{}); !isMap {
				result = appendDescriptionHint(result, fmt.Sprintf("%s: %v", constraint, val))
			}
		}
	}

	if props, ok := result["properties"].(map[string]interface{}); ok {
		newProps := make(map[string]interface{})
		for key, value := range props {
			if propMap, ok := value.(map[string]interface{}); ok {
				newProps[key] = moveConstraintsToDescription(propMap)
			} else {
				newProps[key] = value
			}
		}
		result["properties"] = newProps
	}

	if items, ok := result["items"].(map[string]interface{}); ok {
		result["items"] = moveConstraintsToDescription(items)
	}

	return result
}

func mergeAllOf(schema map[string]interface{}) map[string]interface{} {
	if schema == nil {
		return schema
	}
	result := copyMap(schema)

	if allOf, ok := result["allOf"].([]interface{}); ok && len(allOf) > 0 {
		mergedProps := make(map[string]interface{})
		mergedRequired := make(map[string]bool)
		otherFields := make(map[string]interface{})

		for _, sub := range allOf {
			subSchema, ok := sub.(map[string]interface{})
			if !ok {
				continue
			}

			if props, ok := subSchema["properties"].(map[string]interface{}); ok {
				for k, v := range props {
					mergedProps[k] = v
				}
			}

			if req, ok := subSchema["required"].([]interface{}); ok {
				for _, r := range req {
					if rStr, ok := r.(string); ok {
						mergedRequired[rStr] = true
					}
				}
			}

			for k, v := range subSchema {
				if k != "properties" && k != "required" {
					if _, exists := otherFields[k]; !exists {
						otherFields[k] = v
					}
				}
			}
		}

		delete(result, "allOf")

		for k, v := range otherFields {
			if _, exists := result[k]; !exists {
				result[k] = v
			}
		}

		if len(mergedProps) > 0 {
			if existingProps, ok := result["properties"].(map[string]interface{}); ok {
				for k, v := range existingProps {
					mergedProps[k] = v
				}
			}
			result["properties"] = mergedProps
		}

		if len(mergedRequired) > 0 {
			reqList := make([]interface{}, 0, len(mergedRequired))
			for r := range mergedRequired {
				reqList = append(reqList, r)
			}
			result["required"] = reqList
		}
	}

	if props, ok := result["properties"].(map[string]interface{}); ok {
		newProps := make(map[string]interface{})
		for key, value := range props {
			if propMap, ok := value.(map[string]interface{}); ok {
				newProps[key] = mergeAllOf(propMap)
			} else {
				newProps[key] = value
			}
		}
		result["properties"] = newProps
	}

	if items, ok := result["items"].(map[string]interface{}); ok {
		result["items"] = mergeAllOf(items)
	}

	return result
}

func scoreSchemaOption(schema map[string]interface{}) int {
	if schema == nil {
		return 0
	}
	if _, ok := schema["properties"]; ok {
		return 3
	}
	if t, ok := schema["type"].(string); ok {
		if t == "object" {
			return 3
		}
		if t == "array" {
			return 2
		}
		if t != "null" {
			return 1
		}
	}
	if _, ok := schema["items"]; ok {
		return 2
	}
	return 0
}

func flattenAnyOfOneOf(schema map[string]interface{}) map[string]interface{} {
	if schema == nil {
		return schema
	}
	result := copyMap(schema)

	for _, unionKey := range []string{"anyOf", "oneOf"} {
		if arr, ok := result[unionKey].([]interface{}); ok && len(arr) > 0 {
			var typeNames []string
			var bestOption map[string]interface{}
			bestScore := -1

			for _, opt := range arr {
				optMap, ok := opt.(map[string]interface{})
				if !ok {
					continue
				}

				if t, ok := optMap["type"].(string); ok && t != "null" {
					typeNames = append(typeNames, t)
				} else if _, ok := optMap["properties"]; ok {
					typeNames = append(typeNames, "object")
				}

				score := scoreSchemaOption(optMap)
				if score > bestScore {
					bestScore = score
					bestOption = optMap
				}
			}

			delete(result, unionKey)

			if bestOption != nil {
				parentDesc := ""
				if d, ok := result["description"].(string); ok {
					parentDesc = d
				}

				flattened := flattenAnyOfOneOf(bestOption)
				for k, v := range flattened {
					if k == "description" {
						if desc, ok := v.(string); ok && desc != parentDesc {
							if parentDesc != "" {
								result["description"] = fmt.Sprintf("%s (%s)", parentDesc, desc)
							} else {
								result["description"] = desc
							}
						}
					} else if _, exists := result[k]; !exists || k == "type" || k == "properties" || k == "items" {
						result[k] = v
					}
				}

				if len(typeNames) > 1 {
					unique := uniqueStrings(typeNames)
					result = appendDescriptionHint(result, fmt.Sprintf("Accepts: %s", strings.Join(unique, " | ")))
				}
			}
		}
	}

	if props, ok := result["properties"].(map[string]interface{}); ok {
		newProps := make(map[string]interface{})
		for key, value := range props {
			if propMap, ok := value.(map[string]interface{}); ok {
				newProps[key] = flattenAnyOfOneOf(propMap)
			} else {
				newProps[key] = value
			}
		}
		result["properties"] = newProps
	}

	if items, ok := result["items"].(map[string]interface{}); ok {
		result["items"] = flattenAnyOfOneOf(items)
	}

	return result
}

func flattenTypeArrays(schema map[string]interface{}) map[string]interface{} {
	if schema == nil {
		return schema
	}
	result := copyMap(schema)

	if typeArr, ok := result["type"].([]interface{}); ok {
		hasNull := false
		var nonNullTypes []string
		for _, t := range typeArr {
			if ts, ok := t.(string); ok {
				if ts == "null" {
					hasNull = true
				} else {
					nonNullTypes = append(nonNullTypes, ts)
				}
			}
		}

		if len(nonNullTypes) > 0 {
			result["type"] = nonNullTypes[0]
		} else {
			result["type"] = "string"
		}

		if len(nonNullTypes) > 1 {
			result = appendDescriptionHint(result, fmt.Sprintf("Accepts: %s", strings.Join(nonNullTypes, " | ")))
		}

		if hasNull {
			result = appendDescriptionHint(result, "nullable")
		}
	}

	if props, ok := result["properties"].(map[string]interface{}); ok {
		newProps := make(map[string]interface{})
		for key, value := range props {
			if propMap, ok := value.(map[string]interface{}); ok {
				newProps[key] = flattenTypeArrays(propMap)
			} else {
				newProps[key] = value
			}
		}
		result["properties"] = newProps
	}

	if items, ok := result["items"].(map[string]interface{}); ok {
		result["items"] = flattenTypeArrays(items)
	}

	return result
}

func toGoogleType(t string) string {
	if t == "" {
		return t
	}
	typeMap := map[string]string{
		"string":  "STRING",
		"number":  "NUMBER",
		"integer": "INTEGER",
		"boolean": "BOOLEAN",
		"array":   "ARRAY",
		"object":  "OBJECT",
		"null":    "STRING",
	}
	if upper, ok := typeMap[strings.ToLower(t)]; ok {
		return upper
	}
	return strings.ToUpper(t)
}

func copyMap(m map[string]interface{}) map[string]interface{} {
	result := make(map[string]interface{})
	for k, v := range m {
		result[k] = v
	}
	return result
}

func uniqueStrings(strs []string) []string {
	seen := make(map[string]bool)
	result := make([]string, 0)
	for _, s := range strs {
		if !seen[s] {
			seen[s] = true
			result = append(result, s)
		}
	}
	return result
}
