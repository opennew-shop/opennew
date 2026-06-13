package middleware

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/gin-gonic/gin"
)

// schemaMapping maps API routes to their JSON Schema definition names within schema files.
// Structure: route_path -> {schemaFile, definitionName}
var schemaMapping = map[string]struct {
	schemaFile     string
	definitionName string
}{
	"/api/v1/cli/quote":               {"quote.schema.json", "quote_request"},
	"/api/v1/cli/checkout/prepare":    {"checkout.schema.json", "checkout_prepare_request"},
	"/api/v1/cli/checkout/commit":     {"checkout.schema.json", "checkout_commit_request"},
}

// schemaCache holds parsed JSON schemas in memory.
var (
	schemaCache     = make(map[string]map[string]interface{})
	schemaCacheLock sync.RWMutex
)

// SchemaValidator returns a Gin middleware that validates JSON request bodies
// against the pre-defined JSON Schema for the matched route.
// Schema files are loaded from the given schemasDir.
//
// Validation failures return 400 with detailed error information.
// GET requests are skipped (no request body to validate).
func SchemaValidator(schemasDir string) gin.HandlerFunc {
	// Pre-load all schema files
	loadSchemas(schemasDir)

	return func(c *gin.Context) {
		// Skip validation for non-mutation methods
		if c.Request.Method == "GET" || c.Request.Method == "HEAD" || c.Request.Method == "OPTIONS" {
			c.Next()
			return
		}

		path := c.Request.URL.Path

		// Find the matching schema definition
		mapping, ok := findMatchingSchema(path)
		if !ok {
			// No schema mapped for this path; skip validation
			c.Next()
			return
		}

		// Read request body
		body, err := c.GetRawData()
		if err != nil {
			c.AbortWithStatusJSON(400, gin.H{
				"error": gin.H{
					"code":    "INVALID_REQUEST",
					"message": "Failed to read request body",
					"request_id": c.GetString("request_id"),
				},
			})
			return
		}

		// Parse request body as JSON
		var requestBody map[string]interface{}
		if err := json.Unmarshal(body, &requestBody); err != nil {
			c.AbortWithStatusJSON(400, gin.H{
				"error": gin.H{
					"code":       "INVALID_JSON",
					"message":    "Request body is not valid JSON",
					"details":    err.Error(),
					"request_id": c.GetString("request_id"),
				},
			})
			return
		}

		// Load the schema definition
		schemaCacheLock.RLock()
		schema, schemaExists := schemaCache[mapping.schemaFile]
		schemaCacheLock.RUnlock()

		if !schemaExists {
			// Schema not loaded; skip validation
			c.Next()
			return
		}

		// Extract the specific definition from the schema
		defSchema := extractDefinition(schema, mapping.definitionName)
		if defSchema == nil {
			c.Next()
			return
		}

		// Validate request body against schema
		validationErrors := validateAgainstJSONSchema(defSchema, requestBody, "")
		if len(validationErrors) > 0 {
			details := make([]gin.H, 0, len(validationErrors))
			for _, ve := range validationErrors {
				details = append(details, gin.H{
					"field":  ve.Field,
					"reason": ve.Reason,
				})
			}
			c.AbortWithStatusJSON(400, gin.H{
				"error": gin.H{
					"code":       "SCHEMA_VALIDATION_FAILED",
					"message":    "Request body does not match the expected schema",
					"details":    details,
					"request_id": c.GetString("request_id"),
				},
			})
			return
		}

		// Restore the request body for downstream handlers
		c.Set("validated_body", requestBody)
		c.Set("raw_body", body)
		// Re-set the body so it can be read again by handlers
		c.Request.Body.Close()

		c.Next()
	}
}

// ValidationError represents a single schema validation failure.
type ValidationError struct {
	Field  string `json:"field"`
	Reason string `json:"reason"`
}

// findMatchingSchema finds the most specific schema mapping for the given path.
func findMatchingSchema(path string) (struct {
	schemaFile     string
	definitionName string
}, bool) {
	// Try exact match first
	if m, ok := schemaMapping[path]; ok {
		return m, true
	}

	// Try prefix match (longest prefix wins)
	var bestMatch struct {
		schemaFile     string
		definitionName string
	}
	bestLen := 0
	for routePath, m := range schemaMapping {
		if strings.HasPrefix(path, routePath) && len(routePath) > bestLen {
			bestMatch = m
			bestLen = len(routePath)
		}
	}
	if bestLen > 0 {
		return bestMatch, true
	}

	return struct {
		schemaFile     string
		definitionName string
	}{}, false
}

// loadSchemas pre-loads all schema files from the given directory into memory.
func loadSchemas(schemasDir string) {
	schemaCacheLock.Lock()
	defer schemaCacheLock.Unlock()

	// Collect unique schema files from mapping
	schemaFiles := make(map[string]bool)
	for _, m := range schemaMapping {
		schemaFiles[m.schemaFile] = true
	}

	for sf := range schemaFiles {
		fullPath := filepath.Join(schemasDir, sf)
		data, err := os.ReadFile(fullPath)
		if err != nil {
			// Schema file not found; skip (validation will be skipped at request time)
			continue
		}

		var schema map[string]interface{}
		if err := json.Unmarshal(data, &schema); err != nil {
			continue
		}

		schemaCache[sf] = schema
	}
}

// extractDefinition extracts a named definition from a JSON Schema.
// For ANCF schemas, definitions are under "definitions" key.
func extractDefinition(schema map[string]interface{}, definitionName string) map[string]interface{} {
	if defs, ok := schema["definitions"].(map[string]interface{}); ok {
		if def, ok := defs[definitionName].(map[string]interface{}); ok {
			return def
		}
	}
	// If no definitions key or definition not found, use schema root
	return schema
}

// validateAgainstJSONSchema validates a JSON value against a JSON Schema definition.
// Returns a list of ValidationError if validation fails.
func validateAgainstJSONSchema(schema map[string]interface{}, value interface{}, path string) []ValidationError {
	var errors []ValidationError

	schemaType, hasType := schema["type"].(string)

	// Check required fields
	if required, ok := schema["required"].([]interface{}); ok {
		if obj, isObj := value.(map[string]interface{}); isObj {
			for _, req := range required {
				fieldName, ok := req.(string)
				if !ok {
					continue
				}
				if _, exists := obj[fieldName]; !exists {
					fieldPath := joinPath(path, fieldName)
					errors = append(errors, ValidationError{
						Field:  fieldPath,
						Reason: fmt.Sprintf("required field '%s' is missing", fieldName),
					})
				}
			}
		} else if hasType && schemaType == "object" {
			// Value is not an object but schema expects one
			for _, req := range required {
				fieldName, _ := req.(string)
				errors = append(errors, ValidationError{
					Field:  path,
					Reason: fmt.Sprintf("expected object with required field '%s', got %T", fieldName, value),
				})
			}
		}
	}

	if !hasType {
		return errors
	}

	// Type checking
	switch schemaType {
	case "object":
		obj, ok := value.(map[string]interface{})
		if !ok {
			errors = append(errors, ValidationError{
				Field:  path,
				Reason: fmt.Sprintf("expected object, got %T", value),
			})
			return errors
		}

		// Check properties constraints
		props, hasProps := schema["properties"].(map[string]interface{})
		if !hasProps {
			return errors
		}

		for propName, propSchema := range props {
			propSchemaMap, ok := propSchema.(map[string]interface{})
			if !ok {
				continue
			}
			if propValue, exists := obj[propName]; exists {
				fieldPath := joinPath(path, propName)
				errs := validateAgainstJSONSchema(propSchemaMap, propValue, fieldPath)
				errors = append(errors, errs...)
			}
		}

		// Check additionalProperties: false
		if ap, ok := schema["additionalProperties"]; ok {
			if apBool, ok := ap.(bool); ok && !apBool && hasProps {
				for key := range obj {
					if _, defined := props[key]; !defined {
						errors = append(errors, ValidationError{
							Field:  joinPath(path, key),
							Reason: fmt.Sprintf("additional property '%s' is not allowed", key),
						})
					}
				}
			}
		}

	case "array":
		arr, ok := value.([]interface{})
		if !ok {
			errors = append(errors, ValidationError{
				Field:  path,
				Reason: fmt.Sprintf("expected array, got %T", value),
			})
			return errors
		}

		// Check minItems
		if minItems, ok := schema["minItems"].(float64); ok {
			if float64(len(arr)) < minItems {
				errors = append(errors, ValidationError{
					Field:  path,
					Reason: fmt.Sprintf("array must have at least %d items, got %d", int(minItems), len(arr)),
				})
			}
		}

		// Check items schema
		if items, ok := schema["items"].(map[string]interface{}); ok {
			for i, item := range arr {
				itemPath := fmt.Sprintf("%s[%d]", path, i)
				errs := validateAgainstJSONSchema(items, item, itemPath)
				errors = append(errors, errs...)
			}
		}

	case "string":
		str, ok := value.(string)
		if !ok {
			errors = append(errors, ValidationError{
				Field:  path,
				Reason: fmt.Sprintf("expected string, got %T", value),
			})
			return errors
		}

		// Check minLength
		if minLen, ok := schema["minLength"].(float64); ok {
			if float64(len(str)) < minLen {
				errors = append(errors, ValidationError{
					Field:  path,
					Reason: fmt.Sprintf("string must be at least %d characters, got %d", int(minLen), len(str)),
				})
			}
		}

		// Check pattern
		if pattern, ok := schema["pattern"].(string); ok {
			if matched, _ := filepath.Match(pattern, str); !matched {
				// Note: filepath.Match is limited; full regex would need a regexp library
				// For simple patterns like "^quote_" or "^[0-9]+$" we do basic checking
				if !simplePatternMatch(pattern, str) {
					errors = append(errors, ValidationError{
						Field:  path,
						Reason: fmt.Sprintf("string '%s' does not match pattern '%s'", str, pattern),
					})
				}
			}
		}

		// Check enum
		if enumVals, ok := schema["enum"].([]interface{}); ok {
			found := false
			for _, ev := range enumVals {
				if es, ok := ev.(string); ok && es == str {
					found = true
					break
				}
			}
			if !found {
				enumStrs := make([]string, 0, len(enumVals))
				for _, ev := range enumVals {
					if es, ok := ev.(string); ok {
						enumStrs = append(enumStrs, es)
					}
				}
				errors = append(errors, ValidationError{
					Field:  path,
					Reason: fmt.Sprintf("value '%s' is not one of [%s]", str, strings.Join(enumStrs, ", ")),
				})
			}
		}

	case "integer":
		// JSON numbers are float64 by default in Go's json.Unmarshal
		switch v := value.(type) {
		case float64:
			if v != float64(int64(v)) {
				errors = append(errors, ValidationError{
					Field:  path,
					Reason: fmt.Sprintf("expected integer, got %v", v),
				})
			} else if min, ok := schema["exclusiveMinimum"].(float64); ok {
				if v <= min {
					errors = append(errors, ValidationError{
						Field:  path,
						Reason: fmt.Sprintf("value %v must be greater than %v", v, min),
					})
				}
			}
		default:
			errors = append(errors, ValidationError{
				Field:  path,
				Reason: fmt.Sprintf("expected integer, got %T", value),
			})
		}

	case "boolean":
		if _, ok := value.(bool); !ok {
			errors = append(errors, ValidationError{
				Field:  path,
				Reason: fmt.Sprintf("expected boolean, got %T", value),
			})
		}
	}

	return errors
}

// simplePatternMatch performs basic pattern matching for common JSON Schema patterns.
func simplePatternMatch(pattern, value string) bool {
	// Handle ^prefix patterns
	if strings.HasPrefix(pattern, "^") && strings.HasSuffix(pattern, "$") {
		inner := pattern[1 : len(pattern)-1]
		return strings.HasPrefix(value, inner)
	}
	// Handle ^[0-9]+$ (digits only)
	if pattern == "^[0-9]+$" {
		for _, c := range value {
			if c < '0' || c > '9' {
				return false
			}
		}
		return len(value) > 0
	}
	return true // permissive fallback
}

func joinPath(base, field string) string {
	if base == "" {
		return field
	}
	return base + "." + field
}
