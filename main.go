// main.go
package main

import (
	"bytes"
	"embed"
	"flag"
	"fmt"
	"io/ioutil"
	"log"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"text/template"
	"time"
)

//go:embed templates/*.tmpl
var templateFS embed.FS

var (
	outputDir string
	apiFile   string
	version   bool
	force     bool
)

func init() {
	flag.StringVar(&outputDir, "o", path.Join("output", fmt.Sprintf("api-%d", time.Now().Unix())), "Output directory")
	flag.StringVar(&apiFile, "f", "openapi.yaml", "API file")
	flag.BoolVar(&version, "v", false, "Version")
	flag.BoolVar(&force, "force", false, "Force overwrite output directory; default is false; if true, the output directory will be overwritten")
}

func main() {
	flag.Parse()
	if version {
		fmt.Printf("moonbeam version %s\n", "v0.0.2")
		os.Exit(0)
	}
	// 读取上传的文件内容
	data, err := os.ReadFile(apiFile)
	if err != nil {
		fmt.Printf("❌ failed to read API file: %v\n", err)
		log.Fatal(err)
	}

	api, err := ParseOpenAPI(data)
	if err != nil {
		fmt.Printf("❌ failed to parse OpenAPI: %v\n", err)
		log.Fatal(err)
	}
	if force {
		os.RemoveAll(outputDir)
	}
	// 创建输出目录
	err = os.MkdirAll(outputDir, 0755)
	if err != nil {
		fmt.Printf("❌ create output directory failed: %v\n", err)
		log.Fatal("create output directory failed:", err)
	}

	// 加载模板
	interfaceDefTmpl, err := template.ParseFS(templateFS, "templates/interface-definition.tmpl")
	if err != nil {
		fmt.Printf("❌ failed to parse interface-definition template: %v\n", err)
		log.Fatal(err)
	}

	interfaceTmpl, err := template.ParseFS(templateFS, "templates/interface.tmpl")
	if err != nil {
		fmt.Printf("❌ failed to parse interface template: %v\n", err)
		log.Fatal(err)
	}

	functionTmpl, err := template.ParseFS(templateFS, "templates/function.tmpl")
	if err != nil {
		fmt.Printf("❌ failed to parse function template: %v\n", err)
		log.Fatal(err)
	}

	fileTmpl, err := template.ParseFS(templateFS, "templates/file.tmpl")
	if err != nil {
		fmt.Printf("❌ failed to parse file template: %v\n", err)
		log.Fatal(err)
	}

	indexTmpl, err := template.ParseFS(templateFS, "templates/index.tmpl")
	if err != nil {
		fmt.Printf("❌ failed to parse index template: %v\n", err)
		log.Fatal(err)
	}

	// 按模块组织数据
	modules := make(map[string]*ModuleData)
	interfacesByModule := make(map[string]map[string]string) // module -> interfaceName -> interfaceCode
	functionsByModule := make(map[string]map[string]string)  // module -> functionName -> functionCode
	functionOrder := make(map[string]int)                    // 记录函数处理顺序

	// 缓存所有枚举类型
	enumTypes := make(map[string]bool)
	for name, schema := range api.Components.Schemas {
		if len(schema.Enum) > 0 {
			// 只存储原始名称，保持完整的 ref 名称
			enumTypes[name] = true
		}
	}

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
		interfaceCode := renderInterface(name, schema, interfaceDefTmpl, enumTypes)
		// 只有当接口代码不为空时才添加到映射中
		if interfaceCode != "" {
			interfacesByModule[moduleName][name] = interfaceCode
		}
	}

	// 处理所有API路径
	processedFunctions := make(map[string]bool) // 用于去重
	globalOrder := 0                            // 全局处理顺序计数器

	// 先对路径进行排序，确保处理顺序的一致性
	var sortedPaths []string
	for path := range api.Paths {
		sortedPaths = append(sortedPaths, path)
	}
	sort.Strings(sortedPaths)

	// 为有查询参数的请求生成请求类型（GET, DELETE 等）
	generatedRequestTypes := make(map[string]bool)
	for _, path := range sortedPaths {
		pathItem := api.Paths[path]

		// 处理所有HTTP方法的查询参数
		operations := []struct {
			op     *Operation
			method string
		}{
			{pathItem.Get, "GET"},
			{pathItem.Delete, "DELETE"},
			{pathItem.Put, "PUT"},
			{pathItem.Post, "POST"},
		}

		for _, opData := range operations {
			if opData.op == nil || len(opData.op.Parameters) == 0 {
				continue
			}

			// 只处理有查询参数的请求，且没有 RequestBody 的请求
			if opData.op.RequestBody == nil {
				requestTypeName := generateRequestTypeFromParameters(opData.op.Parameters, opData.op.OperationID)
				if requestTypeName != "EmptyRequest" && !generatedRequestTypes[requestTypeName] {
					generatedRequestTypes[requestTypeName] = true

					// 生成请求类型接口
					requestInterface := generateRequestInterfaceFromParameters(requestTypeName, opData.op.Parameters)
					if requestInterface != "" {
						moduleName := getModuleFromSchemaName("types")
						if _, exists := interfacesByModule[moduleName]; !exists {
							interfacesByModule[moduleName] = make(map[string]string)
						}
						interfacesByModule[moduleName][requestTypeName] = requestInterface
					}
				}
			}
		}
	}

	for _, path := range sortedPaths {
		pathItem := api.Paths[path]
		// 处理所有HTTP方法，而不是只处理第一个
		operations := []struct {
			op     *Operation
			method string
		}{
			{pathItem.Post, "POST"},
			{pathItem.Get, "GET"},
			{pathItem.Put, "PUT"},
			{pathItem.Delete, "DELETE"},
		}

		for _, opData := range operations {
			if opData.op == nil {
				continue
			}

			op := opData.op
			method := opData.method

			if op.OperationID == "" {
				continue
			}

			moduleName := getModuleName(op.Tags)
			if _, exists := modules[moduleName]; !exists {
				modules[moduleName] = &ModuleData{Name: moduleName}
			}

			// 初始化函数映射
			if _, exists := functionsByModule[moduleName]; !exists {
				functionsByModule[moduleName] = make(map[string]string)
			}

			paramType := "EmptyRequest"

			// 优先处理 RequestBody（POST/PUT 请求）
			if op.RequestBody != nil {
				for _, c := range op.RequestBody.Content {
					if c.Schema.RefValue != "" {
						paramType = cleanRef(c.Schema.RefValue)
						break
					}
				}
			} else if len(op.Parameters) > 0 {
				// 处理 Parameters（GET 请求的查询参数）
				paramType = generateRequestTypeFromParameters(op.Parameters, op.OperationID)
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

			// 处理重复的函数名，自动添加编号
			originalFnName := fnName
			counter := 1
			for {
				// 检查这个函数名是否已经在这个模块中被使用过
				fnNameExists := false
				for key := range processedFunctions {
					if strings.HasPrefix(key, fmt.Sprintf("%s_%s_", moduleName, fnName)) {
						fnNameExists = true
						break
					}
				}
				if !fnNameExists {
					break
				}
				// 如果存在，添加编号
				counter++
				fnName = fmt.Sprintf("%s%d", originalFnName, counter)
			}

			// 创建唯一标识符，用于去重 - 使用路径和操作ID的组合
			uniqueKey := fmt.Sprintf("%s_%s_%s_%s", moduleName, fnName, method, path)

			// 如果已经处理过这个函数，跳过
			if processedFunctions[uniqueKey] {
				continue
			}
			processedFunctions[uniqueKey] = true

			funcCode := renderFunction(FunctionData{
				Summary:      summary,
				FunctionName: fnName,
				ParamType:    paramType,
				ResponseType: responseType,
				Method:       strings.ToUpper(method),
				Path:         path,
			}, functionTmpl)

			// 将函数代码存储到临时映射中，使用函数名作为键
			functionsByModule[moduleName][fnName] = funcCode

			// 记录函数处理顺序，确保相同 OperationID 的接口按处理顺序排列
			globalOrder++
			functionOrder[fnName] = globalOrder
		}
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
			fmt.Printf("❌ create module directory failed %s: %v\n", moduleName, err)
			log.Printf("create module directory failed %s: %v", moduleName, err)
			continue
		}

		// 生成接口文件
		var usedEnums []string
		if moduleName == "types" {
			usedEnums = extractUsedEnums(interfaces, enumTypes)
		}

		// 创建排序后的接口名称列表
		var sortedNames []string
		for name := range interfaces {
			sortedNames = append(sortedNames, name)
		}
		sort.Strings(sortedNames)

		interfaceData := InterfaceFileData{
			ModuleName:  moduleName,
			Interfaces:  interfaces,
			UsedEnums:   usedEnums,
			SortedNames: sortedNames,
		}

		var buf bytes.Buffer
		err = interfaceTmpl.Execute(&buf, interfaceData)
		if err != nil {
			fmt.Printf("❌ interface template execution failed %s: %v\n", moduleName, err)
			log.Printf("interface template execution failed %s: %v", moduleName, err)
			continue
		}

		filename := filepath.Join(moduleDir, "index.ts")
		err = ioutil.WriteFile(filename, buf.Bytes(), 0644)
		if err != nil {
			fmt.Printf("❌ write interface file failed %s: %v\n", filename, err)
			log.Printf("write interface file failed %s: %v", filename, err)
		} else {
			fmt.Printf("✅ generate interface file: %s\n", filename)
		}
	}

	// 生成枚举文件
	if len(api.Components.Schemas) > 0 {
		// 收集所有枚举
		var allEnums []EnumData
		for name, schema := range api.Components.Schemas {
			if len(schema.Enum) > 0 {
				enumValues := make([]string, 0, len(schema.Enum))
				for _, value := range schema.Enum {
					if str, ok := value.(string); ok {
						enumValues = append(enumValues, str)
					}
				}

				typeName := cleanRef("#/" + name)
				// 对于枚举类型，保持完整的 ref 名称，不进行简化

				// 对枚举值进行排序
				sort.Strings(enumValues)

				enumData := EnumData{
					SchemaName: name,
					TypeName:   typeName,
					EnumValues: enumValues,
				}
				allEnums = append(allEnums, enumData)
			}
		}

		// 生成枚举文件
		if len(allEnums) > 0 {
			// 对枚举按TypeName排序
			sort.Slice(allEnums, func(i, j int) bool {
				return allEnums[i].TypeName < allEnums[j].TypeName
			})

			enumFileData := struct {
				Enums []EnumData
			}{
				Enums: allEnums,
			}

			enumFileTmpl, err := template.ParseFS(templateFS, "templates/enum-file.tmpl")
			if err == nil {
				var buf bytes.Buffer
				err = enumFileTmpl.Execute(&buf, enumFileData)
				if err == nil {
					typesDir := filepath.Join(outputDir, "types")
					err := os.MkdirAll(typesDir, 0755)
					if err == nil {
						filename := filepath.Join(outputDir, "types", "enum.ts")
						err = ioutil.WriteFile(filename, buf.Bytes(), 0644)
						if err == nil {
							fmt.Printf("✅ generate enum file: %s\n", filename)
						}
					}
				}
			}
		}
	}

	// 将临时映射中的函数按名称排序后添加到模块中
	for moduleName, functions := range functionsByModule {
		if _, exists := modules[moduleName]; !exists {
			modules[moduleName] = &ModuleData{Name: moduleName}
		}

		// 创建排序后的函数名称列表
		// 使用函数名和顺序号的组合来排序，确保相同 OperationID 的接口按处理顺序排列
		type functionSortItem struct {
			name  string
			order int
		}

		var functionSortItems []functionSortItem
		for functionName := range functions {
			order := functionOrder[functionName]
			functionSortItems = append(functionSortItems, functionSortItem{
				name:  functionName,
				order: order,
			})
		}

		// 按函数名排序，如果函数名相同则按处理顺序排序
		sort.Slice(functionSortItems, func(i, j int) bool {
			if functionSortItems[i].name == functionSortItems[j].name {
				return functionSortItems[i].order < functionSortItems[j].order
			}
			return functionSortItems[i].name < functionSortItems[j].name
		})

		var sortedFunctionNames []string
		for _, item := range functionSortItems {
			sortedFunctionNames = append(sortedFunctionNames, item.name)
		}

		// 按排序后的顺序添加函数到模块中
		for _, functionName := range sortedFunctionNames {
			modules[moduleName].Functions = append(modules[moduleName].Functions, functions[functionName])
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
			fmt.Printf("❌ create module directory failed %s: %v\n", name, err)
			log.Printf("create module directory failed %s: %v", name, err)
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
			fmt.Printf("❌ template execution failed %s: %v\n", name, err)
			log.Printf("template execution failed %s: %v", name, err)
			continue
		}

		filename := filepath.Join(moduleDir, "index.ts")
		err = ioutil.WriteFile(filename, buf.Bytes(), 0644)
		if err != nil {
			fmt.Printf("❌ write file failed %s: %v\n", filename, err)
			log.Printf("write file failed %s: %v", filename, err)
		} else {
			fmt.Printf("✅ generate module file: %s\n", filename)
		}
	}

	// 生成根目录的index.ts文件
	rootIndexData := RootIndexData{
		Modules: modules,
	}

	var buf bytes.Buffer
	err = indexTmpl.Execute(&buf, rootIndexData)
	if err != nil {
		fmt.Printf("❌ root index template execution failed: %v\n", err)
		log.Printf("root index template execution failed: %v", err)
	} else {
		filename := filepath.Join(outputDir, "index.ts")
		err = ioutil.WriteFile(filename, buf.Bytes(), 0644)
		if err != nil {
			fmt.Printf("❌ write root index file failed: %v\n", err)
			log.Printf("write root index file failed: %v", err)
		} else {
			fmt.Printf("✅ generate root index file: %s\n", filename)
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

type EnumData struct {
	SchemaName string
	TypeName   string
	EnumValues []string
}

type InterfaceFileData struct {
	ModuleName  string
	Interfaces  map[string]string
	UsedEnums   []string
	SortedNames []string
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

type ProcessedProperty struct {
	Property   Property
	TypeName   string
	IsRequired bool
}

func renderInterface(schemaName string, schema Schema, tmpl *template.Template, enumTypes map[string]bool) string {
	// 提取接口名称，不包含命名空间前缀
	typeName := cleanRef("#/" + schemaName)
	// 如果typeName包含点号，只取最后一部分
	if strings.Contains(typeName, ".") {
		parts := strings.Split(typeName, ".")
		typeName = parts[len(parts)-1]
	}

	var buf bytes.Buffer

	// 检查是否为枚举类型
	if len(schema.Enum) > 0 {
		// 枚举类型将在单独的enum.ts文件中生成，这里返回空字符串
		return ""
	}

	// 确保Properties不为nil
	properties := schema.Properties
	if properties == nil {
		properties = make(map[string]Property)
	}

	// 预处理所有属性的类型名称
	processedProperties := make(map[string]ProcessedProperty)
	for key, prop := range properties {
		processedProperties[key] = ProcessedProperty{
			Property:   prop,
			TypeName:   prop.TypeName(enumTypes),
			IsRequired: prop.IsRequired(),
		}
	}

	data := struct {
		SchemaName string
		TypeName   string
		Properties map[string]ProcessedProperty
	}{
		SchemaName: schemaName,
		TypeName:   typeName,
		Properties: processedProperties,
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
	err := tmpl.Execute(&buf, newData)
	if err != nil {
		fmt.Printf("❌ failed to execute function template for %s: %v\n", data.FunctionName, err)
		log.Printf("failed to execute function template for %s: %v", data.FunctionName, err)
	}
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
			// 使用 map 来去重接口名称
			uniqueInterfaces := make(map[string]bool)
			var neededInterfaces []string

			for _, cleanName := range interfaces {
				if usedInterfaces[cleanName] && !uniqueInterfaces[cleanName] {
					uniqueInterfaces[cleanName] = true
					neededInterfaces = append(neededInterfaces, cleanName)
				}
			}

			if len(neededInterfaces) > 0 {
				// 对接口名称进行排序
				sort.Strings(neededInterfaces)
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
// extractUsedEnums 从接口代码中提取使用的枚举类型
func extractUsedEnums(interfaces map[string]string, enumTypes map[string]bool) []string {
	usedEnums := make(map[string]bool)

	// 遍历所有接口代码，查找使用的枚举类型
	for _, code := range interfaces {
		// 使用正则表达式匹配类型定义中的枚举类型
		// 匹配模式：fieldName?: EnumTypeName 或 fieldName?: EnumTypeName[]
		re := regexp.MustCompile(`\w+\??:\s*([A-Z][a-zA-Z_]*)(?:\[\])?`)
		matches := re.FindAllStringSubmatch(code, -1)

		for _, match := range matches {
			if len(match) > 1 {
				typeName := match[1]
				// 检查是否为真正的枚举类型
				if enumTypes[typeName] {
					usedEnums[typeName] = true
				}
			}
		}
	}

	// 转换为切片并排序
	var result []string
	for enumName := range usedEnums {
		result = append(result, enumName)
	}

	// 使用标准库排序
	sort.Strings(result)

	return result
}

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

// generateRequestTypeFromParameters 根据参数生成请求类型名称
func generateRequestTypeFromParameters(parameters []Parameter, operationID string) string {
	if len(parameters) == 0 {
		return "EmptyRequest"
	}

	// 从 operationID 中提取操作名称，例如 "Team_GetTeamRole" -> "GetTeamRole"
	parts := strings.Split(operationID, "_")
	if len(parts) < 2 {
		return "EmptyRequest"
	}

	operationName := parts[1]
	return operationName + "Request"
}

// generateRequestInterfaceFromParameters 根据参数生成请求接口代码
func generateRequestInterfaceFromParameters(typeName string, parameters []Parameter) string {
	if len(parameters) == 0 {
		return ""
	}

	var properties []string
	for _, param := range parameters {
		if param.In != "query" {
			continue // 只处理查询参数
		}

		// 确定 TypeScript 类型
		var tsType string
		if param.Schema.Ref != "" {
			tsType = cleanRef(param.Schema.Ref)
		} else {
			switch param.Schema.Type {
			case "string":
				tsType = "string"
			case "integer", "number":
				tsType = "number"
			case "boolean":
				tsType = "boolean"
			default:
				tsType = "any"
			}
		}

		// 生成属性定义
		optional := "?"
		if param.Required {
			optional = ""
		}

		description := ""
		if param.Description != "" {
			description = fmt.Sprintf("  /**\n   * %s\n   */\n  ", param.Description)
		}

		// 处理属性名中的点号，转换为下划线
		propertyName := strings.ReplaceAll(param.Name, ".", "_")
		property := fmt.Sprintf("%s%s%s: %s", description, propertyName, optional, tsType)
		properties = append(properties, property)
	}

	if len(properties) == 0 {
		return ""
	}

	// 生成完整的接口代码
	interfaceCode := fmt.Sprintf("/**\n * %s\n */\nexport interface %s {\n", typeName, typeName)
	for _, prop := range properties {
		interfaceCode += fmt.Sprintf("  %s\n", prop)
	}
	interfaceCode += "}\n"

	return interfaceCode
}
