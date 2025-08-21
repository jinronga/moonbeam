// main.go
package main

import (
	"bytes"
	"flag"
	"fmt"
	"io/ioutil"
	"log"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"
	"text/template"
	"time"
)

var outputDir string
var apiFile string

func init() {
	flag.StringVar(&outputDir, "output", "output", "Output directory")
	flag.StringVar(&apiFile, "api", "api.yaml", "API file")
}

func main() {
	flag.Parse()
	// 读取上传的文件内容
	data, err := os.ReadFile(apiFile)
	if err != nil {
		log.Fatal(err)
	}

	api, err := ParseOpenAPI(data)
	if err != nil {
		log.Fatal(err)
	}
	outputDir = path.Join(outputDir, fmt.Sprintf("api-%d", time.Now().Unix()))
	// 创建输出目录
	err = os.MkdirAll(outputDir, 0755)
	if err != nil {
		log.Fatal("创建输出目录失败:", err)
	}

	// 加载模板
	interfaceDefTmpl := template.Must(template.ParseFiles("templates/interface-definition.tmpl"))
	interfaceTmpl := template.Must(template.ParseFiles("templates/interface.tmpl"))
	functionTmpl := template.Must(template.ParseFiles("templates/function.tmpl"))
	fileTmpl := template.Must(template.ParseFiles("templates/file.tmpl"))
	indexTmpl := template.Must(template.ParseFiles("templates/index.tmpl"))

	// 按模块组织数据
	modules := make(map[string]*ModuleData)
	interfacesByModule := make(map[string]map[string]string) // module -> interfaceName -> interfaceCode

	// 处理所有接口定义
	for name, schema := range api.Components.Schemas {
		moduleName := getModuleFromSchemaName(name)
		if _, exists := modules[moduleName]; !exists {
			modules[moduleName] = &ModuleData{Name: moduleName}
		}

		// 初始化接口映射
		if _, exists := interfacesByModule[moduleName]; !exists {
			interfacesByModule[moduleName] = make(map[string]string)
		}

		// 生成接口代码
		interfaceCode := renderInterface(name, schema, interfaceDefTmpl)
		interfacesByModule[moduleName][name] = interfaceCode
	}

	// 处理所有API路径
	for path, pathItem := range api.Paths {
		var op *Operation
		var method string
		if pathItem.Post != nil {
			op = pathItem.Post
			method = "POST"
		} else if pathItem.Get != nil {
			op = pathItem.Get
			method = "GET"
		} else if pathItem.Put != nil {
			op = pathItem.Put
			method = "PUT"
		} else {
			continue
		}

		if op.OperationID == "" {
			continue
		}

		moduleName := getModuleName(op.Tags)
		if _, exists := modules[moduleName]; !exists {
			modules[moduleName] = &ModuleData{Name: moduleName}
		}

		paramType := "EmptyRequest"
		if op.RequestBody != nil {
			for _, c := range op.RequestBody.Content {
				if c.Schema.RefValue != "" {
					paramType = cleanRef(c.Schema.RefValue)
					break
				}
			}
		}

		responseType := "EmptyReply"
		if resp, ok := op.Responses["200"]; ok {
			for _, c := range resp.Content {
				if c.Schema.RefValue != "" {
					responseType = cleanRef(c.Schema.RefValue)
					break
				}
			}
		}

		summary := op.Summary
		if summary == "" && len(op.Tags) > 0 {
			summary = strings.Split(op.OperationID, "_")[1] + " " + strings.Join(op.Tags, ", ")
		}

		fnName := toCamel(strings.Split(op.OperationID, "_")[1])
		fnName = strings.ToLower(fnName[:1]) + fnName[1:]

		funcCode := renderFunction(FunctionData{
			Summary:      summary,
			FunctionName: fnName,
			ParamType:    paramType,
			ResponseType: responseType,
			Method:       strings.ToUpper(method),
			Path:         path,
		}, functionTmpl)

		modules[moduleName].Functions = append(modules[moduleName].Functions, funcCode)
	}

	// 首先生成所有接口文件
	for moduleName, interfaces := range interfacesByModule {
		if len(interfaces) == 0 {
			continue
		}

		// 创建模块目录
		moduleDir := filepath.Join(outputDir, moduleName)
		err := os.MkdirAll(moduleDir, 0755)
		if err != nil {
			log.Printf("创建模块目录失败 %s: %v", moduleName, err)
			continue
		}

		// 生成接口文件
		interfaceData := InterfaceFileData{
			ModuleName: moduleName,
			Interfaces: interfaces,
		}

		var buf bytes.Buffer
		err = interfaceTmpl.Execute(&buf, interfaceData)
		if err != nil {
			log.Printf("接口模板执行失败 %s: %v", moduleName, err)
			continue
		}

		filename := filepath.Join(moduleDir, "index.ts")
		err = ioutil.WriteFile(filename, buf.Bytes(), 0644)
		if err != nil {
			log.Printf("写入接口文件失败 %s: %v", filename, err)
		} else {
			fmt.Printf("✅ 生成接口文件: %s\n", filename)
		}
	}

	// 生成每个模块的API文件
	for name, mod := range modules {
		if len(mod.Functions) == 0 {
			continue
		}

		// 创建模块目录（如果不存在）
		moduleDir := filepath.Join(outputDir, name)
		err := os.MkdirAll(moduleDir, 0755)
		if err != nil {
			log.Printf("创建模块目录失败 %s: %v", name, err)
			continue
		}

		// 准备文件数据，包含导入语句
		fileData := FileData{
			ModuleName: name,
			Functions:  mod.Functions,
			Imports:    generateImports(name, interfacesByModule, mod.Functions),
		}

		var buf bytes.Buffer
		err = fileTmpl.Execute(&buf, fileData)
		if err != nil {
			log.Printf("模板执行失败 %s: %v", name, err)
			continue
		}

		filename := filepath.Join(moduleDir, "api.ts")
		err = ioutil.WriteFile(filename, buf.Bytes(), 0644)
		if err != nil {
			log.Printf("写入文件失败 %s: %v", filename, err)
		} else {
			fmt.Printf("✅ 生成API文件: %s\n", filename)
		}
	}

	// 生成根目录的index.ts文件
	rootIndexData := RootIndexData{
		Modules: modules,
	}

	var buf bytes.Buffer
	err = indexTmpl.Execute(&buf, rootIndexData)
	if err != nil {
		log.Printf("根索引模板执行失败: %v", err)
	} else {
		filename := filepath.Join(outputDir, "index.ts")
		err = ioutil.WriteFile(filename, buf.Bytes(), 0644)
		if err != nil {
			log.Printf("写入根索引文件失败: %v", err)
		} else {
			fmt.Printf("✅ 生成根索引文件: %s\n", filename)
		}
	}
}

type ModuleData struct {
	Name       string
	Interfaces []string
	Functions  []string
}

type FunctionData struct {
	Summary      string
	FunctionName string
	ParamType    string
	ResponseType string
	Method       string
	Path         string
}

type InterfaceFileData struct {
	ModuleName string
	Interfaces map[string]string
}

type FileData struct {
	ModuleName string
	Functions  []string
	Imports    []ImportData
}

type ImportData struct {
	Module     string
	Interfaces []string
}

type RootIndexData struct {
	Modules map[string]*ModuleData
}

func renderInterface(schemaName string, schema Schema, tmpl *template.Template) string {
	// 提取接口名称，不包含命名空间前缀
	typeName := cleanRef("#/" + schemaName)
	// 如果typeName包含点号，只取最后一部分
	if strings.Contains(typeName, ".") {
		parts := strings.Split(typeName, ".")
		typeName = parts[len(parts)-1]
	}

	var buf bytes.Buffer

	// 确保Properties不为nil
	properties := schema.Properties
	if properties == nil {
		properties = make(map[string]Property)
	}

	// 创建新的Properties映射，处理类型名称
	cleanProperties := make(map[string]Property)
	for key, prop := range properties {
		// 复制属性，但清理类型名称
		cleanProp := prop
		if prop.Ref != "" {
			// 清理引用类型名称
			cleanTypeName := cleanRef(prop.Ref)
			if strings.Contains(cleanTypeName, ".") {
				parts := strings.Split(cleanTypeName, ".")
				cleanTypeName = parts[len(parts)-1]
			}
			cleanProp.Ref = cleanTypeName
		}
		cleanProperties[key] = cleanProp
	}

	data := struct {
		SchemaName string
		TypeName   string
		Properties map[string]Property
	}{
		SchemaName: schemaName,
		TypeName:   typeName,
		Properties: cleanProperties,
	}
	tmpl.Execute(&buf, data)
	return buf.String()
}

func renderFunction(data FunctionData, tmpl *template.Template) string {
	// 处理类型名称，移除命名空间前缀
	paramType := data.ParamType
	if strings.Contains(paramType, ".") {
		parts := strings.Split(paramType, ".")
		paramType = parts[len(parts)-1]
	}

	responseType := data.ResponseType
	if strings.Contains(responseType, ".") {
		parts := strings.Split(responseType, ".")
		responseType = parts[len(parts)-1]
	}

	// 创建新的FunctionData，使用处理后的类型名称
	newData := FunctionData{
		Summary:      data.Summary,
		FunctionName: data.FunctionName,
		ParamType:    paramType,
		ResponseType: responseType,
		Method:       data.Method,
		Path:         data.Path,
	}

	var buf bytes.Buffer
	tmpl.Execute(&buf, newData)
	return buf.String()
}

func toCamel(s string) string {
	parts := strings.Split(s, "_")
	for i, p := range parts {
		if i == 0 {
			continue
		}
		if len(p) > 0 {
			parts[i] = strings.ToUpper(p[:1]) + strings.ToLower(p[1:])
		}
	}
	return strings.Join(parts, "")
}

func getModuleFromSchemaName(schemaName string) string {
	// 所有接口都归入同一个模块，让API调用时按tag来分组
	return "types"
}

func generateImports(moduleName string, interfacesByModule map[string]map[string]string, functions []string) []ImportData {
	var imports []ImportData

	// 收集所有需要导入的接口（清理后的名称）
	allInterfaces := make(map[string]map[string]string) // module -> originalName -> cleanName

	for module, interfaces := range interfacesByModule {
		cleanMap := make(map[string]string)
		for originalName := range interfaces {
			// 清理接口名称，移除命名空间前缀
			cleanName := cleanRef("#/" + originalName)
			if strings.Contains(cleanName, ".") {
				parts := strings.Split(cleanName, ".")
				cleanName = parts[len(parts)-1]
			}
			cleanMap[originalName] = cleanName
		}
		if len(cleanMap) > 0 {
			allInterfaces[module] = cleanMap
		}
	}

	// 分析API函数中实际使用的接口类型
	usedInterfaces := make(map[string]bool)

	// 从函数代码中提取使用的类型
	for _, funcCode := range functions {
		// 提取参数类型和返回类型
		extractUsedTypes(funcCode, usedInterfaces)
	}

	// 对于API模块，只导入实际使用的接口
	if moduleName != "types" {
		if interfaces, exists := allInterfaces["types"]; exists {
			var neededInterfaces []string
			for _, cleanName := range interfaces {
				if usedInterfaces[cleanName] {
					neededInterfaces = append(neededInterfaces, cleanName)
				}
			}
			if len(neededInterfaces) > 0 {
				imports = append(imports, ImportData{
					Module:     "types",
					Interfaces: neededInterfaces,
				})
			}
		}
	}

	return imports
}

// extractUsedTypes 从函数代码中提取使用的类型名称
func extractUsedTypes(funcCode string, usedInterfaces map[string]bool) {
	// 提取参数类型：@param { TypeName } params
	paramPattern := `@param\s*\{\s*([^}]+)\s*\}\s*params`
	paramMatches := regexp.MustCompile(paramPattern).FindStringSubmatch(funcCode)
	if len(paramMatches) > 1 {
		typeName := strings.TrimSpace(paramMatches[1])
		usedInterfaces[typeName] = true
	}

	// 提取返回类型：@returns {Promise<TypeName>}
	returnPattern := `@returns\s*\{Promise<([^>]+)>\}`
	returnMatches := regexp.MustCompile(returnPattern).FindStringSubmatch(funcCode)
	if len(returnMatches) > 1 {
		typeName := strings.TrimSpace(returnMatches[1])
		usedInterfaces[typeName] = true
	}

	// 提取函数签名中的类型：function name(params: TypeName): Promise<TypeName>
	sigPattern := `function\s+\w+\(params:\s*([^)]+)\):\s*Promise<([^>]+)>`
	sigMatches := regexp.MustCompile(sigPattern).FindStringSubmatch(funcCode)
	if len(sigMatches) > 2 {
		paramType := strings.TrimSpace(sigMatches[1])
		returnType := strings.TrimSpace(sigMatches[2])
		usedInterfaces[paramType] = true
		usedInterfaces[returnType] = true
	}
}
