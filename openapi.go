// openapi.go
package main

import (
	"strings"

	"gopkg.in/yaml.v3"
)

type OpenAPI struct {
	Paths      map[string]PathItem `yaml:"paths"`
	Components struct {
		Schemas map[string]Schema `yaml:"schemas"`
	} `yaml:"components"`
}

type PathItem struct {
	Post *Operation `yaml:"post"`
	Get  *Operation `yaml:"get"`
	Put  *Operation `yaml:"put"`
}

type Operation struct {
	Tags        []string `yaml:"tags"`
	Summary     string   `yaml:"summary"`
	OperationID string   `yaml:"operationId"`
	RequestBody *struct {
		Content map[string]struct {
			Schema Ref `yaml:"schema"`
		} `yaml:"content"`
	} `yaml:"requestBody"`
	Responses map[string]struct {
		Content map[string]struct {
			Schema Ref `yaml:"schema"`
		} `yaml:"content"`
	} `yaml:"responses"`
}

type Schema struct {
	Type                 string                      `yaml:"type"`
	Properties           map[string]Property         `yaml:"properties"`
	AdditionalProperties *AdditionalPropertiesSchema `yaml:"additionalProperties"`
	Description          string                      `yaml:"description"`
	Format               string                      `yaml:"format"`
	Items                *Ref                        `yaml:"items"`
	AllOf                []Ref                       `yaml:"allOf"`
}

type Property struct {
	Type                 string                      `yaml:"type"`
	Format               string                      `yaml:"format"`
	Description          string                      `yaml:"description"`
	Ref                  string                      `yaml:"$ref"`
	AllOf                []Ref                       `yaml:"allOf"`
	Items                *Ref                        `yaml:"items"`
	AdditionalProperties *AdditionalPropertiesSchema `yaml:"additionalProperties"`
}

type AdditionalPropertiesSchema struct {
	Type string `yaml:"type"`
}

type Ref struct {
	RefValue string `yaml:"$ref"`
}

func ParseOpenAPI(data []byte) (*OpenAPI, error) {
	var api OpenAPI
	err := yaml.Unmarshal(data, &api)
	return &api, err
}

func (p Property) IsRequired() bool {
	return false // 可扩展为从 requestBody.required 获取
}

func (p Property) TypeName() string {
	if p.Ref != "" {
		typeName := cleanRef(p.Ref)
		// 清理命名空间前缀
		if strings.Contains(typeName, ".") {
			parts := strings.Split(typeName, ".")
			typeName = parts[len(parts)-1]
		}
		return typeName
	}
	if len(p.AllOf) > 0 {
		typeName := cleanRef(p.AllOf[0].RefValue)
		// 清理命名空间前缀
		if strings.Contains(typeName, ".") {
			parts := strings.Split(typeName, ".")
			typeName = parts[len(parts)-1]
		}
		return typeName
	}
	if p.Type == "array" && p.Items != nil {
		typeName := cleanRef(p.Items.RefValue)
		// 清理命名空间前缀
		if strings.Contains(typeName, ".") {
			parts := strings.Split(typeName, ".")
			typeName = parts[len(parts)-1]
		}
		return typeName + "[]"
	}
	if p.Type == "object" && p.AdditionalProperties != nil && p.AdditionalProperties.Type == "string" {
		return "{ [key: string]: string }"
	}
	switch p.Type {
	case "string":
		return "string"
	case "integer":
		return "number"
	case "number":
		return "number"
	case "boolean":
		return "boolean"
	case "object":
		return "object"
	default:
		return "any"
	}
}

func cleanRef(ref string) string {
	parts := strings.Split(ref, "/")
	return parts[len(parts)-1]
}

func getModuleName(tags []string) string {
	if len(tags) > 0 {
		return strings.ToLower(tags[0])
	}
	return "common"
}
