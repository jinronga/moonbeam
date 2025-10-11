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
	Post   *Operation `yaml:"post"`
	Get    *Operation `yaml:"get"`
	Put    *Operation `yaml:"put"`
	Delete *Operation `yaml:"delete"`
}

type Operation struct {
	Tags        []string    `yaml:"tags"`
	Summary     string      `yaml:"summary"`
	OperationID string      `yaml:"operationId"`
	Parameters  []Parameter `yaml:"parameters"`
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
	Enum                 []interface{}               `yaml:"enum"`
}

type Property struct {
	Type                 string                      `yaml:"type"`
	Format               string                      `yaml:"format"`
	Description          string                      `yaml:"description"`
	Ref                  string                      `yaml:"$ref"`
	AllOf                []Ref                       `yaml:"allOf"`
	Items                *Ref                        `yaml:"items"`
	AdditionalProperties *AdditionalPropertiesSchema `yaml:"additionalProperties"`
	Enum                 []interface{}               `yaml:"enum"`
}

type AdditionalPropertiesSchema struct {
	Type string `yaml:"type"`
}

type Parameter struct {
	Name        string `yaml:"name"`
	In          string `yaml:"in"`
	Description string `yaml:"description"`
	Required    bool   `yaml:"required"`
	Schema      struct {
		Type   string `yaml:"type"`
		Format string `yaml:"format"`
		Ref    string `yaml:"$ref"`
	} `yaml:"schema"`
}

type Ref struct {
	RefValue string `yaml:"$ref"`
	Type     string `yaml:"type"`
}

func ParseOpenAPI(data []byte) (*OpenAPI, error) {
	var api OpenAPI
	err := yaml.Unmarshal(data, &api)
	return &api, err
}

func (p Property) IsRequired() bool {
	return false // 可扩展为从 requestBody.required 获取
}

func (p Property) TypeName(enumTypes map[string]bool) string {
	if p.Ref != "" {
		typeName := cleanRef(p.Ref)

		// 如果是枚举类型，直接返回完整的 ref 名称
		if enumTypes[typeName] {
			return typeName
		}

		// 清理命名空间前缀（非枚举类型）
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
		// 处理引用类型
		if p.Items.RefValue != "" {
			typeName := cleanRef(p.Items.RefValue)
			// 清理命名空间前缀
			if strings.Contains(typeName, ".") {
				parts := strings.Split(typeName, ".")
				typeName = parts[len(parts)-1]
			}
			return typeName + "[]"
		}
		// 处理普通类型
		if p.Items.Type != "" {
			switch p.Items.Type {
			case "string":
				return "string[]"
			case "integer":
				return "number[]"
			case "number":
				return "number[]"
			case "boolean":
				return "boolean[]"
			default:
				return "any[]"
			}
		}
		return "any[]"
	}
	if p.Type == "object" && p.AdditionalProperties != nil && p.AdditionalProperties.Type == "string" {
		return "{ [key: string]: string }"
	}
	// 检查是否为枚举类型
	if len(p.Enum) > 0 {
		return "string" // 枚举类型在TypeScript中通常表示为string
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
