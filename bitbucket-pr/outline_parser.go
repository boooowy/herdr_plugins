package main

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"unicode"

	sitter "github.com/tree-sitter/go-tree-sitter"
	tsgolang "github.com/tree-sitter/tree-sitter-go/bindings/go"
	tsjava "github.com/tree-sitter/tree-sitter-java/bindings/go"
	tsjavascript "github.com/tree-sitter/tree-sitter-javascript/bindings/go"
	tspython "github.com/tree-sitter/tree-sitter-python/bindings/go"
	tsrust "github.com/tree-sitter/tree-sitter-rust/bindings/go"
	tstypescript "github.com/tree-sitter/tree-sitter-typescript/bindings/go"
)

type outlineLanguage struct {
	Name     string
	Language func() *sitter.Language
}

func outlineLanguageForPath(path string) (outlineLanguage, bool) {
	ext := strings.ToLower(filepath.Ext(path))
	switch ext {
	case ".go":
		return outlineLanguage{"Go", func() *sitter.Language { return sitter.NewLanguage(tsgolang.Language()) }}, true
	case ".rs":
		return outlineLanguage{"Rust", func() *sitter.Language { return sitter.NewLanguage(tsrust.Language()) }}, true
	case ".py", ".pyi":
		return outlineLanguage{"Python", func() *sitter.Language { return sitter.NewLanguage(tspython.Language()) }}, true
	case ".ts", ".mts", ".cts":
		return outlineLanguage{"TypeScript", func() *sitter.Language { return sitter.NewLanguage(tstypescript.LanguageTypescript()) }}, true
	case ".tsx":
		return outlineLanguage{"TypeScript", func() *sitter.Language { return sitter.NewLanguage(tstypescript.LanguageTSX()) }}, true
	case ".js", ".mjs", ".cjs":
		return outlineLanguage{"JavaScript", func() *sitter.Language { return sitter.NewLanguage(tsjavascript.Language()) }}, true
	case ".jsx":
		// The JavaScript grammar includes JSX productions.
		return outlineLanguage{"JavaScript", func() *sitter.Language { return sitter.NewLanguage(tsjavascript.Language()) }}, true
	case ".java":
		return outlineLanguage{"Java", func() *sitter.Language { return sitter.NewLanguage(tsjava.Language()) }}, true
	default:
		return outlineLanguage{}, false
	}
}

type rawOutlineSymbol struct {
	Path      string
	Language  string
	Name      string
	Kind      string
	Signature string
	StartLine int
	EndLine   int
	StartByte uint
	EndByte   uint
	Calls     []string
	Test      bool
}

func (s rawOutlineSymbol) pairKey() string { return s.Kind + "\x00" + s.Name }

func parseOutlineFile(path string, source []byte) ([]rawOutlineSymbol, string, error) {
	lang, ok := outlineLanguageForPath(path)
	if !ok {
		return nil, "unsupported language", nil
	}
	if generatedSource(source) {
		return nil, "generated", nil
	}
	parser := sitter.NewParser()
	defer parser.Close()
	if err := parser.SetLanguage(lang.Language()); err != nil {
		return nil, "", fmt.Errorf("%s parser: %w", lang.Name, err)
	}
	tree := parser.Parse(source, nil)
	if tree == nil {
		return nil, "", fmt.Errorf("%s parser returned no tree", lang.Name)
	}
	defer tree.Close()

	var symbols []rawOutlineSymbol
	var walk func(*sitter.Node)
	walk = func(node *sitter.Node) {
		if node == nil {
			return
		}
		if kind, nameNode, ok := outlineSymbolNode(lang.Name, node, source); ok {
			name := strings.TrimSpace(nameNode.Utf8Text(source))
			if name != "" {
				start, end := int(node.StartPosition().Row)+1, int(node.EndPosition().Row)+1
				symbols = append(symbols, rawOutlineSymbol{
					Path:      filepath.ToSlash(path),
					Language:  lang.Name,
					Name:      name,
					Kind:      kind,
					Signature: outlineNodeSignature(lang.Name, node, source),
					StartLine: start,
					EndLine:   max(start, end),
					StartByte: node.StartByte(),
					EndByte:   node.EndByte(),
					Calls:     outlineNodeCalls(lang.Name, node, source),
					Test:      outlineTestSymbol(path, node, source),
				})
			}
		}
		for i := uint(0); i < node.NamedChildCount(); i++ {
			walk(node.NamedChild(i))
		}
	}
	walk(tree.RootNode())
	sort.SliceStable(symbols, func(i, j int) bool {
		if symbols[i].StartByte != symbols[j].StartByte {
			return symbols[i].StartByte < symbols[j].StartByte
		}
		return symbols[i].EndByte < symbols[j].EndByte
	})
	return symbols, "", nil
}

func outlineSymbolNode(language string, node *sitter.Node, source []byte) (string, *sitter.Node, bool) {
	kind := node.Kind()
	name := node.ChildByFieldName("name")
	switch language {
	case "Go":
		switch kind {
		case "function_declaration":
			return "fn", name, name != nil
		case "method_declaration":
			return "method", name, name != nil
		case "type_spec":
			return goTypeKind(node, source), name, name != nil
		}
	case "Rust":
		switch kind {
		case "function_item":
			return "fn", name, name != nil
		case "struct_item":
			return "struct", name, name != nil
		case "enum_item":
			return "enum", name, name != nil
		case "trait_item":
			return "trait", name, name != nil
		case "type_item":
			return "type", name, name != nil
		case "const_item", "static_item":
			return "const", name, name != nil
		}
	case "Python":
		switch kind {
		case "function_definition":
			return "fn", name, name != nil
		case "class_definition":
			return "class", name, name != nil
		}
	case "TypeScript", "JavaScript":
		switch kind {
		case "function_declaration", "function_signature", "generator_function_declaration":
			return "fn", name, name != nil
		case "method_definition", "method_signature", "abstract_method_signature":
			return "method", name, name != nil
		case "class_declaration", "abstract_class_declaration":
			return "class", name, name != nil
		case "interface_declaration":
			return "interface", name, name != nil
		case "type_alias_declaration":
			return "type", name, name != nil
		case "enum_declaration":
			return "enum", name, name != nil
		case "variable_declarator":
			value := node.ChildByFieldName("value")
			if value != nil && (value.Kind() == "arrow_function" || value.Kind() == "function_expression") {
				return "fn", name, name != nil
			}
		}
	case "Java":
		switch kind {
		case "method_declaration":
			return "method", name, name != nil
		case "constructor_declaration":
			return "ctor", name, name != nil
		case "class_declaration":
			return "class", name, name != nil
		case "interface_declaration":
			return "interface", name, name != nil
		case "enum_declaration":
			return "enum", name, name != nil
		case "record_declaration":
			return "record", name, name != nil
		case "annotation_type_declaration":
			return "annotation", name, name != nil
		}
	}
	return "", nil, false
}

func goTypeKind(node *sitter.Node, source []byte) string {
	typeNode := node.ChildByFieldName("type")
	if typeNode == nil {
		return "type"
	}
	switch typeNode.Kind() {
	case "struct_type":
		return "struct"
	case "interface_type":
		return "interface"
	default:
		return "type"
	}
}

func outlineNodeSignature(language string, node *sitter.Node, source []byte) string {
	end := node.EndByte()
	if body := node.ChildByFieldName("body"); body != nil && outlineCallableKind(language, node.Kind()) {
		end = body.StartByte()
	} else if node.Kind() == "variable_declarator" {
		// Arrow functions have the parameters on the value node; include the
		// declaration through the arrow but not its block body.
		if value := node.ChildByFieldName("value"); value != nil {
			if body := value.ChildByFieldName("body"); body != nil {
				end = body.StartByte()
			}
		}
	}
	if end < node.StartByte() || int(end) > len(source) {
		end = node.EndByte()
	}
	text := string(source[node.StartByte():end])
	text = strings.TrimSpace(strings.TrimSuffix(text, "{"))
	return normalizeSignature(text)
}

func outlineCallableKind(language, kind string) bool {
	switch kind {
	case "function_declaration", "method_declaration", "function_item", "function_definition",
		"generator_function_declaration", "method_definition", "constructor_declaration":
		return true
	}
	return false
}

func outlineNodeCalls(language string, symbol *sitter.Node, source []byte) []string {
	var calls []string
	var walk func(*sitter.Node, bool)
	walk = func(node *sitter.Node, root bool) {
		if node == nil {
			return
		}
		if !root {
			if _, _, nested := outlineSymbolNode(language, node, source); nested {
				return // nested methods/functions own their calls
			}
		}
		var target *sitter.Node
		switch node.Kind() {
		case "call_expression", "call":
			target = node.ChildByFieldName("function")
		case "method_invocation":
			target = node.ChildByFieldName("name")
		case "object_creation_expression":
			target = node.ChildByFieldName("type")
		}
		if name := outlineReferenceName(target, source); usefulOutlineName(name) {
			calls = append(calls, name)
		}
		for i := uint(0); i < node.NamedChildCount(); i++ {
			walk(node.NamedChild(i), false)
		}
	}
	walk(symbol, true)
	sort.Strings(calls)
	out := calls[:0]
	for _, call := range calls {
		if len(out) == 0 || out[len(out)-1] != call {
			out = append(out, call)
		}
	}
	return out
}

func outlineReferenceName(node *sitter.Node, source []byte) string {
	if node == nil {
		return ""
	}
	switch node.Kind() {
	case "identifier", "field_identifier", "property_identifier", "type_identifier":
		return strings.TrimSpace(node.Utf8Text(source))
	}
	// Member/qualified expressions put the callable name at the right edge.
	for i := int(node.NamedChildCount()) - 1; i >= 0; i-- {
		if name := outlineReferenceName(node.NamedChild(uint(i)), source); name != "" {
			return name
		}
	}
	return ""
}

func usefulOutlineName(name string) bool {
	if len([]rune(name)) <= 1 || name == "_" {
		return false
	}
	for _, r := range name {
		if unicode.IsLetter(r) || r == '_' {
			return true
		}
	}
	return false
}

func generatedSource(source []byte) bool {
	lines := strings.SplitN(string(source), "\n", 6)
	for i, line := range lines {
		if i >= 5 {
			break
		}
		lower := strings.ToLower(line)
		if strings.Contains(lower, "@generated") ||
			(strings.Contains(line, "Code generated") && strings.Contains(line, "DO NOT EDIT")) {
			return true
		}
	}
	return false
}

func outlineTestSymbol(path string, node *sitter.Node, source []byte) bool {
	p := "/" + strings.ToLower(filepath.ToSlash(path))
	base := strings.ToLower(filepath.Base(path))
	switch strings.ToLower(filepath.Ext(path)) {
	case ".go":
		return strings.HasSuffix(base, "_test.go")
	case ".py", ".pyi":
		return strings.Contains(p, "/tests/") || strings.HasPrefix(base, "test_") || strings.Contains(base, "_test.")
	case ".ts", ".tsx", ".js", ".jsx", ".mts", ".cts", ".mjs", ".cjs":
		return strings.Contains(p, "/__tests__/") || strings.Contains(base, ".test.") || strings.Contains(base, ".spec.")
	case ".rs":
		if strings.Contains(p, "/tests/") {
			return true
		}
		start := int(node.StartByte()) - 160
		if start < 0 {
			start = 0
		}
		return strings.Contains(string(source[start:node.StartByte()]), "#[test]")
	case ".java":
		return strings.Contains(p, "/src/test/") || strings.Contains(p, "/test/") || strings.HasSuffix(base, "test.java")
	}
	return false
}

// directlyTouchedSymbols maps changed lines to their smallest enclosing
// symbol. This prevents a changed Java method body from also marking its
// containing class as body-only noise.
func directlyTouchedSymbols(symbols []rawOutlineSymbol, lines map[int]bool) map[int]bool {
	touched := map[int]bool{}
	for line := range lines {
		best, span := -1, int(^uint(0)>>1)
		for i, symbol := range symbols {
			if line < symbol.StartLine || line > symbol.EndLine {
				continue
			}
			if n := symbol.EndLine - symbol.StartLine; n < span {
				best, span = i, n
			}
		}
		if best >= 0 {
			touched[best] = true
		}
	}
	return touched
}
