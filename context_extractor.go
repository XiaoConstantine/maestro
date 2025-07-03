package main

import (
	"context"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"strconv"
	"strings"

	"github.com/XiaoConstantine/dspy-go/pkg/logging"
)

// FileContext represents the comprehensive file-level context for enhanced code review.
type FileContext struct {
	PackageDeclaration string                `json:"package_declaration"`
	Imports            []string              `json:"imports"`
	TypeDefinitions    []TypeDefinition      `json:"type_definitions"`
	Interfaces         []InterfaceDefinition `json:"interfaces"`
	Functions          []FunctionSignature   `json:"functions"`
	Methods            []MethodSignature     `json:"methods"`
	Constants          []ConstantDefinition  `json:"constants"`
	Variables          []VariableDefinition  `json:"variables"`
}

// TypeDefinition represents a type definition in the file.
type TypeDefinition struct {
	Name       string            `json:"name"`
	Type       string            `json:"type"` // struct, interface, alias, etc.
	Fields     []FieldDefinition `json:"fields,omitempty"`
	Methods    []string          `json:"methods,omitempty"`
	DocComment string            `json:"doc_comment,omitempty"`
	LineNumber int               `json:"line_number"`
	Visibility string            `json:"visibility"` // public, private
}

// FieldDefinition represents a struct field.
type FieldDefinition struct {
	Name       string `json:"name"`
	Type       string `json:"type"`
	Tag        string `json:"tag,omitempty"`
	DocComment string `json:"doc_comment,omitempty"`
}

// InterfaceDefinition represents an interface definition.
type InterfaceDefinition struct {
	Name          string            `json:"name"`
	Methods       []InterfaceMethod `json:"methods"`
	EmbeddedTypes []string          `json:"embedded_types,omitempty"`
	DocComment    string            `json:"doc_comment,omitempty"`
	LineNumber    int               `json:"line_number"`
	Visibility    string            `json:"visibility"`
}

// InterfaceMethod represents a method in an interface.
type InterfaceMethod struct {
	Name       string      `json:"name"`
	Parameters []Parameter `json:"parameters,omitempty"`
	Returns    []Parameter `json:"returns,omitempty"`
	DocComment string      `json:"doc_comment,omitempty"`
}

// FunctionSignature represents a function signature.
type FunctionSignature struct {
	Name       string      `json:"name"`
	Parameters []Parameter `json:"parameters,omitempty"`
	Returns    []Parameter `json:"returns,omitempty"`
	DocComment string      `json:"doc_comment,omitempty"`
	LineNumber int         `json:"line_number"`
	Visibility string      `json:"visibility"`
}

// MethodSignature represents a method signature with receiver.
type MethodSignature struct {
	Name       string      `json:"name"`
	Receiver   Parameter   `json:"receiver"`
	Parameters []Parameter `json:"parameters,omitempty"`
	Returns    []Parameter `json:"returns,omitempty"`
	DocComment string      `json:"doc_comment,omitempty"`
	LineNumber int         `json:"line_number"`
	Visibility string      `json:"visibility"`
}

// Parameter represents a function/method parameter or return value.
type Parameter struct {
	Name string `json:"name,omitempty"`
	Type string `json:"type"`
}

// ConstantDefinition represents a constant declaration.
type ConstantDefinition struct {
	Name       string `json:"name"`
	Type       string `json:"type,omitempty"`
	Value      string `json:"value,omitempty"`
	DocComment string `json:"doc_comment,omitempty"`
	LineNumber int    `json:"line_number"`
	Visibility string `json:"visibility"`
}

// VariableDefinition represents a variable declaration.
type VariableDefinition struct {
	Name       string `json:"name"`
	Type       string `json:"type,omitempty"`
	Value      string `json:"value,omitempty"`
	DocComment string `json:"doc_comment,omitempty"`
	LineNumber int    `json:"line_number"`
	Visibility string `json:"visibility"`
}

// ChunkDependencies represents the dependencies and references within a code chunk.
type ChunkDependencies struct {
	CalledFunctions  []FunctionSignature `json:"called_functions"`
	UsedTypes        []TypeReference     `json:"used_types"`
	ImportedPackages []string            `json:"imported_packages"`
	SemanticPurpose  string              `json:"semantic_purpose"`
}

// TypeReference represents a reference to a type within code.
type TypeReference struct {
	Name       string `json:"name"`
	Package    string `json:"package,omitempty"`
	Usage      string `json:"usage"` // instantiation, method_call, type_assertion, etc.
	LineNumber int    `json:"line_number"`
}

// using AST parsing to provide enhanced metadata for code review.
func ExtractFileContext(ctx context.Context, filePath string, content string) (*FileContext, error) {
	logger := logging.GetLogger()

	if !isContextExtractionEnabled() {
		logger.Debug(ctx, "Context extraction disabled via environment variable")
		return &FileContext{}, nil
	}

	logger.Debug(ctx, "Extracting file context for: %s", filePath)

	// Parse the Go source code into an AST
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, filePath, content, parser.ParseComments)
	if err != nil {
		logger.Warn(ctx, "Failed to parse Go file %s: %v", filePath, err)
		return &FileContext{}, fmt.Errorf("failed to parse Go file: %w", err)
	}

	fileContext := &FileContext{
		Imports:         make([]string, 0),
		TypeDefinitions: make([]TypeDefinition, 0),
		Interfaces:      make([]InterfaceDefinition, 0),
		Functions:       make([]FunctionSignature, 0),
		Methods:         make([]MethodSignature, 0),
		Constants:       make([]ConstantDefinition, 0),
		Variables:       make([]VariableDefinition, 0),
	}

	// Extract package declaration
	if file.Name != nil {
		fileContext.PackageDeclaration = file.Name.Name
		logger.Debug(ctx, "Found package declaration: %s", fileContext.PackageDeclaration)
	}

	// Extract imports
	for _, importSpec := range file.Imports {
		importPath := strings.Trim(importSpec.Path.Value, `"`)
		if importSpec.Name != nil {
			// Handle aliased imports
			importPath = importSpec.Name.Name + " " + importPath
		}
		fileContext.Imports = append(fileContext.Imports, importPath)
	}
	logger.Debug(ctx, "Found %d imports", len(fileContext.Imports))

	// Walk through all declarations in the file
	for _, decl := range file.Decls {
		switch d := decl.(type) {
		case *ast.GenDecl:
			extractGenericDeclaration(ctx, d, fset, fileContext, logger)
		case *ast.FuncDecl:
			extractFunctionDeclaration(ctx, d, fset, fileContext, logger)
		}
	}

	logger.Debug(ctx, "Context extraction completed: %d types, %d interfaces, %d functions, %d methods",
		len(fileContext.TypeDefinitions), len(fileContext.Interfaces),
		len(fileContext.Functions), len(fileContext.Methods))

	return fileContext, nil
}

// extractGenericDeclaration handles type, const, and var declarations.
func extractGenericDeclaration(ctx context.Context, decl *ast.GenDecl, fset *token.FileSet,
	fileContext *FileContext, logger *logging.Logger) {

	for _, spec := range decl.Specs {
		switch s := spec.(type) {
		case *ast.TypeSpec:
			extractTypeSpecification(ctx, s, decl, fset, fileContext, logger)
		case *ast.ValueSpec:
			extractValueSpecification(ctx, s, decl, fset, fileContext, logger)
		}
	}
}

// extractTypeSpecification handles type definitions (structs, interfaces, aliases).
func extractTypeSpecification(ctx context.Context, spec *ast.TypeSpec, decl *ast.GenDecl,
	fset *token.FileSet, fileContext *FileContext, logger *logging.Logger) {

	pos := fset.Position(spec.Pos())
	visibility := determineVisibility(spec.Name.Name)
	docComment := extractDocComment(spec.Doc)
	if docComment == "" && decl.Doc != nil {
		docComment = extractDocComment(decl.Doc)
	}

	switch t := spec.Type.(type) {
	case *ast.StructType:
		typeDef := TypeDefinition{
			Name:       spec.Name.Name,
			Type:       "struct",
			Fields:     extractStructFields(t),
			DocComment: docComment,
			LineNumber: pos.Line,
			Visibility: visibility,
		}
		fileContext.TypeDefinitions = append(fileContext.TypeDefinitions, typeDef)
		logger.Debug(ctx, "Found struct type: %s with %d fields", spec.Name.Name, len(typeDef.Fields))

	case *ast.InterfaceType:
		interfaceDef := InterfaceDefinition{
			Name:          spec.Name.Name,
			Methods:       extractInterfaceMethods(t),
			EmbeddedTypes: extractEmbeddedTypes(t),
			DocComment:    docComment,
			LineNumber:    pos.Line,
			Visibility:    visibility,
		}
		fileContext.Interfaces = append(fileContext.Interfaces, interfaceDef)
		logger.Debug(ctx, "Found interface: %s with %d methods", spec.Name.Name, len(interfaceDef.Methods))

	default:
		// Handle type aliases and other type definitions
		typeDef := TypeDefinition{
			Name:       spec.Name.Name,
			Type:       "alias",
			DocComment: docComment,
			LineNumber: pos.Line,
			Visibility: visibility,
		}
		fileContext.TypeDefinitions = append(fileContext.TypeDefinitions, typeDef)
		logger.Debug(ctx, "Found type alias: %s", spec.Name.Name)
	}
}

// extractValueSpecification handles const and var declarations.
func extractValueSpecification(ctx context.Context, spec *ast.ValueSpec, decl *ast.GenDecl,
	fset *token.FileSet, fileContext *FileContext, logger *logging.Logger) {

	pos := fset.Position(spec.Pos())
	docComment := extractDocComment(spec.Doc)
	if docComment == "" && decl.Doc != nil {
		docComment = extractDocComment(decl.Doc)
	}

	for i, name := range spec.Names {
		visibility := determineVisibility(name.Name)

		var typeStr, valueStr string
		if spec.Type != nil {
			typeStr = extractTypeExpression(spec.Type)
		}
		if i < len(spec.Values) && spec.Values[i] != nil {
			valueStr = extractValueExpression(spec.Values[i])
		}

		switch decl.Tok {
		case token.CONST:
			constDef := ConstantDefinition{
				Name:       name.Name,
				Type:       typeStr,
				Value:      valueStr,
				DocComment: docComment,
				LineNumber: pos.Line,
				Visibility: visibility,
			}
			fileContext.Constants = append(fileContext.Constants, constDef)
			logger.Debug(ctx, "Found constant: %s", name.Name)
		case token.VAR:
			varDef := VariableDefinition{
				Name:       name.Name,
				Type:       typeStr,
				Value:      valueStr,
				DocComment: docComment,
				LineNumber: pos.Line,
				Visibility: visibility,
			}
			fileContext.Variables = append(fileContext.Variables, varDef)
			logger.Debug(ctx, "Found variable: %s", name.Name)
		}
	}
}

// extractFunctionDeclaration handles function and method declarations.
func extractFunctionDeclaration(ctx context.Context, decl *ast.FuncDecl, fset *token.FileSet,
	fileContext *FileContext, logger *logging.Logger) {

	pos := fset.Position(decl.Pos())
	visibility := determineVisibility(decl.Name.Name)
	docComment := extractDocComment(decl.Doc)

	parameters := extractParameters(decl.Type.Params)
	returns := extractParameters(decl.Type.Results)

	if decl.Recv != nil && len(decl.Recv.List) > 0 {
		// This is a method
		receiver := extractReceiver(decl.Recv.List[0])
		methodSig := MethodSignature{
			Name:       decl.Name.Name,
			Receiver:   receiver,
			Parameters: parameters,
			Returns:    returns,
			DocComment: docComment,
			LineNumber: pos.Line,
			Visibility: visibility,
		}
		fileContext.Methods = append(fileContext.Methods, methodSig)
		logger.Debug(ctx, "Found method: %s.%s", receiver.Type, decl.Name.Name)
	} else {
		// This is a function
		funcSig := FunctionSignature{
			Name:       decl.Name.Name,
			Parameters: parameters,
			Returns:    returns,
			DocComment: docComment,
			LineNumber: pos.Line,
			Visibility: visibility,
		}
		fileContext.Functions = append(fileContext.Functions, funcSig)
		logger.Debug(ctx, "Found function: %s", decl.Name.Name)
	}
}

// ExtractChunkDependencies analyzes a code chunk to identify dependencies and semantic purpose.
func ExtractChunkDependencies(ctx context.Context, chunkContent string, fileContext *FileContext) (*ChunkDependencies, error) {
	logger := logging.GetLogger()

	if !isDependencyAnalysisEnabled() {
		logger.Debug(ctx, "Dependency analysis disabled via environment variable")
		return &ChunkDependencies{}, nil
	}

	logger.Debug(ctx, "Analyzing chunk dependencies")

	// Parse the chunk content as Go code
	fset := token.NewFileSet()
	// Wrap chunk in a function to make it parseable if it's not a complete declaration
	wrappedContent := fmt.Sprintf("package main\nfunc _temp() {\n%s\n}", chunkContent)
	file, err := parser.ParseFile(fset, "", wrappedContent, 0)
	if err != nil {
		// If parsing fails, try parsing as is
		file, err = parser.ParseFile(fset, "", chunkContent, 0)
		if err != nil {
			logger.Debug(ctx, "Failed to parse chunk content: %v", err)
			return &ChunkDependencies{}, nil
		}
	}

	dependencies := &ChunkDependencies{
		CalledFunctions:  make([]FunctionSignature, 0),
		UsedTypes:        make([]TypeReference, 0),
		ImportedPackages: make([]string, 0),
	}

	// Walk the AST to find function calls and type usage
	ast.Inspect(file, func(n ast.Node) bool {
		switch node := n.(type) {
		case *ast.CallExpr:
			// Extract function calls
			if funcName := extractFunctionCallName(node); funcName != "" {
				// Check if this function exists in our file context
				for _, fn := range fileContext.Functions {
					if fn.Name == funcName {
						dependencies.CalledFunctions = append(dependencies.CalledFunctions, fn)
						break
					}
				}
				// Also check methods
				for _, method := range fileContext.Methods {
					if method.Name == funcName {
						funcSig := FunctionSignature{
							Name:       method.Name,
							Parameters: method.Parameters,
							Returns:    method.Returns,
							DocComment: method.DocComment,
							LineNumber: method.LineNumber,
							Visibility: method.Visibility,
						}
						dependencies.CalledFunctions = append(dependencies.CalledFunctions, funcSig)
						break
					}
				}
			}
		case *ast.SelectorExpr:
			// Extract type usage and package references
			if ident, ok := node.X.(*ast.Ident); ok {
				typeRef := TypeReference{
					Name:       node.Sel.Name,
					Package:    ident.Name,
					Usage:      "method_call",
					LineNumber: fset.Position(node.Pos()).Line,
				}
				dependencies.UsedTypes = append(dependencies.UsedTypes, typeRef)
			}
		case *ast.CompositeLit:
			// Extract type instantiations
			if typeName := extractTypeFromCompositeLit(node); typeName != "" {
				typeRef := TypeReference{
					Name:       typeName,
					Usage:      "instantiation",
					LineNumber: fset.Position(node.Pos()).Line,
				}
				dependencies.UsedTypes = append(dependencies.UsedTypes, typeRef)
			}
		}
		return true
	})

	// Generate semantic purpose based on the chunk content and dependencies
	dependencies.SemanticPurpose = generateSemanticPurpose(chunkContent, dependencies, fileContext)

	logger.Debug(ctx, "Found %d function calls, %d type references",
		len(dependencies.CalledFunctions), len(dependencies.UsedTypes))

	return dependencies, nil
}

// Helper functions for AST parsing and extraction

// extractStructFields extracts field definitions from a struct type.
func extractStructFields(structType *ast.StructType) []FieldDefinition {
	fields := make([]FieldDefinition, 0)

	if structType.Fields == nil {
		return fields
	}

	for _, field := range structType.Fields.List {
		typeStr := extractTypeExpression(field.Type)
		docComment := extractDocComment(field.Doc)

		var tagStr string
		if field.Tag != nil {
			tagStr = field.Tag.Value
		}

		if len(field.Names) == 0 {
			// Embedded field
			fields = append(fields, FieldDefinition{
				Name:       typeStr, // Use type as name for embedded fields
				Type:       typeStr,
				Tag:        tagStr,
				DocComment: docComment,
			})
		} else {
			for _, name := range field.Names {
				fields = append(fields, FieldDefinition{
					Name:       name.Name,
					Type:       typeStr,
					Tag:        tagStr,
					DocComment: docComment,
				})
			}
		}
	}

	return fields
}

// extractInterfaceMethods extracts method definitions from an interface type.
func extractInterfaceMethods(interfaceType *ast.InterfaceType) []InterfaceMethod {
	methods := make([]InterfaceMethod, 0)

	if interfaceType.Methods == nil {
		return methods
	}

	for _, method := range interfaceType.Methods.List {
		if len(method.Names) == 0 {
			// Embedded interface - skip for now
			continue
		}

		for _, name := range method.Names {
			if funcType, ok := method.Type.(*ast.FuncType); ok {
				methodDef := InterfaceMethod{
					Name:       name.Name,
					Parameters: extractParameters(funcType.Params),
					Returns:    extractParameters(funcType.Results),
					DocComment: extractDocComment(method.Doc),
				}
				methods = append(methods, methodDef)
			}
		}
	}

	return methods
}

// extractEmbeddedTypes extracts embedded types from an interface.
func extractEmbeddedTypes(interfaceType *ast.InterfaceType) []string {
	embedded := make([]string, 0)

	if interfaceType.Methods == nil {
		return embedded
	}

	for _, method := range interfaceType.Methods.List {
		if len(method.Names) == 0 {
			// This is an embedded type
			typeStr := extractTypeExpression(method.Type)
			embedded = append(embedded, typeStr)
		}
	}

	return embedded
}

// extractParameters extracts parameter definitions from a field list.
func extractParameters(fieldList *ast.FieldList) []Parameter {
	if fieldList == nil {
		return nil
	}

	params := make([]Parameter, 0)
	for _, field := range fieldList.List {
		typeStr := extractTypeExpression(field.Type)

		if len(field.Names) == 0 {
			// Unnamed parameter
			params = append(params, Parameter{
				Type: typeStr,
			})
		} else {
			for _, name := range field.Names {
				params = append(params, Parameter{
					Name: name.Name,
					Type: typeStr,
				})
			}
		}
	}

	return params
}

// extractReceiver extracts receiver information from a method.
func extractReceiver(field *ast.Field) Parameter {
	typeStr := extractTypeExpression(field.Type)

	if len(field.Names) > 0 {
		return Parameter{
			Name: field.Names[0].Name,
			Type: typeStr,
		}
	}

	return Parameter{
		Type: typeStr,
	}
}

// extractTypeExpression converts an AST type expression to a string representation.
func extractTypeExpression(expr ast.Expr) string {
	if expr == nil {
		return ""
	}

	switch e := expr.(type) {
	case *ast.Ident:
		return e.Name
	case *ast.StarExpr:
		return "*" + extractTypeExpression(e.X)
	case *ast.ArrayType:
		return "[]" + extractTypeExpression(e.Elt)
	case *ast.MapType:
		return "map[" + extractTypeExpression(e.Key) + "]" + extractTypeExpression(e.Value)
	case *ast.ChanType:
		direction := ""
		switch e.Dir {
		case ast.SEND:
			direction = "chan<- "
		case ast.RECV:
			direction = "<-chan "
		default:
			direction = "chan "
		}
		return direction + extractTypeExpression(e.Value)
	case *ast.FuncType:
		return "func" + extractFunctionSignature(e)
	case *ast.InterfaceType:
		return "interface{}"
	case *ast.StructType:
		return "struct{}"
	case *ast.SelectorExpr:
		return extractTypeExpression(e.X) + "." + e.Sel.Name
	case *ast.Ellipsis:
		return "..." + extractTypeExpression(e.Elt)
	default:
		return "unknown"
	}
}

// extractFunctionSignature extracts function signature for func types.
func extractFunctionSignature(funcType *ast.FuncType) string {
	params := "("
	if funcType.Params != nil {
		for i, param := range funcType.Params.List {
			if i > 0 {
				params += ", "
			}
			params += extractTypeExpression(param.Type)
		}
	}
	params += ")"

	returns := ""
	if funcType.Results != nil && len(funcType.Results.List) > 0 {
		returns = " "
		if len(funcType.Results.List) > 1 {
			returns += "("
		}
		for i, result := range funcType.Results.List {
			if i > 0 {
				returns += ", "
			}
			returns += extractTypeExpression(result.Type)
		}
		if len(funcType.Results.List) > 1 {
			returns += ")"
		}
	}

	return params + returns
}

// extractValueExpression converts an AST value expression to a string representation.
func extractValueExpression(expr ast.Expr) string {
	if expr == nil {
		return ""
	}

	switch e := expr.(type) {
	case *ast.BasicLit:
		return e.Value
	case *ast.Ident:
		return e.Name
	case *ast.SelectorExpr:
		return extractValueExpression(e.X) + "." + e.Sel.Name
	case *ast.CallExpr:
		return extractFunctionCallName(e) + "()"
	default:
		return "complex_expression"
	}
}

// extractFunctionCallName extracts the function name from a call expression.
func extractFunctionCallName(callExpr *ast.CallExpr) string {
	switch fn := callExpr.Fun.(type) {
	case *ast.Ident:
		return fn.Name
	case *ast.SelectorExpr:
		return fn.Sel.Name
	default:
		return ""
	}
}

// extractTypeFromCompositeLit extracts type name from composite literal.
func extractTypeFromCompositeLit(lit *ast.CompositeLit) string {
	if lit.Type != nil {
		return extractTypeExpression(lit.Type)
	}
	return ""
}

// extractDocComment extracts documentation comment text.
func extractDocComment(commentGroup *ast.CommentGroup) string {
	if commentGroup == nil {
		return ""
	}

	var comments []string
	for _, comment := range commentGroup.List {
		text := strings.TrimPrefix(comment.Text, "//")
		text = strings.TrimPrefix(text, "/*")
		text = strings.TrimSuffix(text, "*/")
		text = strings.TrimSpace(text)
		if text != "" {
			comments = append(comments, text)
		}
	}

	return strings.Join(comments, " ")
}

// determineVisibility determines if an identifier is public or private.
func determineVisibility(name string) string {
	if len(name) > 0 && name[0] >= 'A' && name[0] <= 'Z' {
		return "public"
	}
	return "private"
}

// generateSemanticPurpose creates a high-level description of what the code chunk does.
func generateSemanticPurpose(chunkContent string, dependencies *ChunkDependencies, fileContext *FileContext) string {
	// Simple heuristic-based semantic purpose generation
	content := strings.ToLower(chunkContent)

	// Check for common patterns
	if strings.Contains(content, "func test") || strings.Contains(content, "testing.") {
		return "Unit test function that validates specific functionality"
	}

	if strings.Contains(content, "http.") || strings.Contains(content, "handler") || strings.Contains(content, "router") {
		return "HTTP handler or web service endpoint implementation"
	}

	if strings.Contains(content, "sql") || strings.Contains(content, "database") || strings.Contains(content, "db.") {
		return "Database operation or query execution"
	}

	if strings.Contains(content, "json") || strings.Contains(content, "marshal") || strings.Contains(content, "unmarshal") {
		return "Data serialization or deserialization logic"
	}

	if strings.Contains(content, "error") || strings.Contains(content, "return") && strings.Contains(content, "err") {
		return "Error handling and validation logic"
	}

	if strings.Contains(content, "log") || strings.Contains(content, "print") {
		return "Logging or debugging output functionality"
	}

	if strings.Contains(content, "struct") || strings.Contains(content, "type") {
		return "Type definition or data structure declaration"
	}

	if strings.Contains(content, "interface") {
		return "Interface definition establishing contracts"
	}

	if strings.Contains(content, "const") {
		return "Constant declarations and configuration values"
	}

	if strings.Contains(content, "var") {
		return "Variable declarations and initialization"
	}

	if len(dependencies.CalledFunctions) > 3 {
		return "Complex business logic with multiple function interactions"
	}

	if len(dependencies.CalledFunctions) > 0 {
		return "Business logic implementation with function calls"
	}

	// Default semantic purpose
	return "Code implementation with specific functionality"
}

// Format helper functions for enhanced chunk metadata

// FormatFileContextForLLM formats file context into LLM-friendly strings.
func FormatFileContextForLLM(fileContext *FileContext) map[string]string {
	if fileContext == nil {
		return map[string]string{}
	}

	return map[string]string{
		"package_name":        fileContext.PackageDeclaration,
		"imports":             strings.Join(fileContext.Imports, "\n"),
		"type_definitions":    formatEnhancedTypeDefinitions(fileContext.TypeDefinitions),
		"interfaces":          formatEnhancedInterfaces(fileContext.Interfaces),
		"function_signatures": formatEnhancedFunctionSignatures(fileContext.Functions),
		"method_signatures":   formatEnhancedMethodSignatures(fileContext.Methods),
		"constants":           formatConstants(fileContext.Constants),
		"variables":           formatVariables(fileContext.Variables),
	}
}

// FormatChunkDependenciesForLLM formats chunk dependencies into LLM-friendly strings.
func FormatChunkDependenciesForLLM(dependencies *ChunkDependencies) map[string]string {
	if dependencies == nil {
		return map[string]string{}
	}

	return map[string]string{
		"called_functions":  formatEnhancedFunctionSignatures(dependencies.CalledFunctions),
		"used_types":        formatTypeReferences(dependencies.UsedTypes),
		"imported_packages": strings.Join(dependencies.ImportedPackages, "\n"),
		"semantic_purpose":  dependencies.SemanticPurpose,
	}
}

// formatEnhancedTypeDefinitions formats type definitions for LLM consumption.
func formatEnhancedTypeDefinitions(types []TypeDefinition) string {
	if len(types) == 0 {
		return ""
	}

	var formatted []string
	for _, typeDef := range types {
		typeStr := fmt.Sprintf("type %s %s", typeDef.Name, typeDef.Type)
		if typeDef.Type == "struct" && len(typeDef.Fields) > 0 {
			typeStr += " {"
			for _, field := range typeDef.Fields {
				fieldStr := fmt.Sprintf("  %s %s", field.Name, field.Type)
				if field.Tag != "" {
					fieldStr += " " + field.Tag
				}
				typeStr += "\n" + fieldStr
			}
			typeStr += "\n}"
		}
		if typeDef.DocComment != "" {
			typeStr = fmt.Sprintf("// %s\n%s", typeDef.DocComment, typeStr)
		}
		formatted = append(formatted, typeStr)
	}

	return strings.Join(formatted, "\n\n")
}

// formatEnhancedInterfaces formats interface definitions for LLM consumption.
func formatEnhancedInterfaces(interfaces []InterfaceDefinition) string {
	if len(interfaces) == 0 {
		return ""
	}

	var formatted []string
	for _, iface := range interfaces {
		ifaceStr := fmt.Sprintf("type %s interface {", iface.Name)
		for _, method := range iface.Methods {
			params := formatParameters(method.Parameters)
			returns := formatParameters(method.Returns)
			methodStr := fmt.Sprintf("  %s(%s)", method.Name, params)
			if returns != "" {
				methodStr += " " + returns
			}
			ifaceStr += "\n" + methodStr
		}
		ifaceStr += "\n}"
		if iface.DocComment != "" {
			ifaceStr = fmt.Sprintf("// %s\n%s", iface.DocComment, ifaceStr)
		}
		formatted = append(formatted, ifaceStr)
	}

	return strings.Join(formatted, "\n\n")
}

// formatEnhancedFunctionSignatures formats function signatures for LLM consumption.
func formatEnhancedFunctionSignatures(functions []FunctionSignature) string {
	if len(functions) == 0 {
		return ""
	}

	var formatted []string
	for _, fn := range functions {
		params := formatParameters(fn.Parameters)
		returns := formatParameters(fn.Returns)
		funcStr := fmt.Sprintf("func %s(%s)", fn.Name, params)
		if returns != "" {
			funcStr += " " + returns
		}
		if fn.DocComment != "" {
			funcStr = fmt.Sprintf("// %s\n%s", fn.DocComment, funcStr)
		}
		formatted = append(formatted, funcStr)
	}

	return strings.Join(formatted, "\n")
}

// formatEnhancedMethodSignatures formats method signatures for LLM consumption.
func formatEnhancedMethodSignatures(methods []MethodSignature) string {
	if len(methods) == 0 {
		return ""
	}

	var formatted []string
	for _, method := range methods {
		receiver := fmt.Sprintf("(%s %s)", method.Receiver.Name, method.Receiver.Type)
		params := formatParameters(method.Parameters)
		returns := formatParameters(method.Returns)
		methodStr := fmt.Sprintf("func %s %s(%s)", receiver, method.Name, params)
		if returns != "" {
			methodStr += " " + returns
		}
		if method.DocComment != "" {
			methodStr = fmt.Sprintf("// %s\n%s", method.DocComment, methodStr)
		}
		formatted = append(formatted, methodStr)
	}

	return strings.Join(formatted, "\n")
}

// formatConstants formats constant definitions for LLM consumption.
func formatConstants(constants []ConstantDefinition) string {
	if len(constants) == 0 {
		return ""
	}

	var formatted []string
	for _, constant := range constants {
		constStr := fmt.Sprintf("const %s", constant.Name)
		if constant.Type != "" {
			constStr += " " + constant.Type
		}
		if constant.Value != "" {
			constStr += " = " + constant.Value
		}
		if constant.DocComment != "" {
			constStr = fmt.Sprintf("// %s\n%s", constant.DocComment, constStr)
		}
		formatted = append(formatted, constStr)
	}

	return strings.Join(formatted, "\n")
}

// formatVariables formats variable definitions for LLM consumption.
func formatVariables(variables []VariableDefinition) string {
	if len(variables) == 0 {
		return ""
	}

	var formatted []string
	for _, variable := range variables {
		varStr := fmt.Sprintf("var %s", variable.Name)
		if variable.Type != "" {
			varStr += " " + variable.Type
		}
		if variable.Value != "" {
			varStr += " = " + variable.Value
		}
		if variable.DocComment != "" {
			varStr = fmt.Sprintf("// %s\n%s", variable.DocComment, varStr)
		}
		formatted = append(formatted, varStr)
	}

	return strings.Join(formatted, "\n")
}

// formatParameters formats parameter lists for LLM consumption.
func formatParameters(params []Parameter) string {
	if len(params) == 0 {
		return ""
	}

	var formatted []string
	for _, param := range params {
		if param.Name != "" {
			formatted = append(formatted, fmt.Sprintf("%s %s", param.Name, param.Type))
		} else {
			formatted = append(formatted, param.Type)
		}
	}

	return strings.Join(formatted, ", ")
}

// formatTypeReferences formats type references for LLM consumption.
func formatTypeReferences(typeRefs []TypeReference) string {
	if len(typeRefs) == 0 {
		return ""
	}

	var formatted []string
	for _, typeRef := range typeRefs {
		refStr := typeRef.Name
		if typeRef.Package != "" {
			refStr = typeRef.Package + "." + refStr
		}
		refStr += fmt.Sprintf(" (%s)", typeRef.Usage)
		formatted = append(formatted, refStr)
	}

	return strings.Join(formatted, "\n")
}

// Environment variable support functions

// isContextExtractionEnabled checks if context extraction is enabled via environment variable.
func isContextExtractionEnabled() bool {
	if value := os.Getenv("MAESTRO_CONTEXT_EXTRACTION_ENABLED"); value != "" {
		enabled, err := strconv.ParseBool(value)
		if err == nil {
			return enabled
		}
	}
	return true // Default enabled
}


// isDependencyAnalysisEnabled checks if dependency analysis is enabled.
func isDependencyAnalysisEnabled() bool {
	if value := os.Getenv("MAESTRO_ENABLE_DEPENDENCY_ANALYSIS"); value != "" {
		enabled, err := strconv.ParseBool(value)
		if err == nil {
			return enabled
		}
	}
	return true // Default enabled
}
