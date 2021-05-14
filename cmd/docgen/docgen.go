// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at http://mozilla.org/MPL/2.0/.
// Taken from https://github.com/talos-systems/talos/blob/master/hack/docgen/main.go
package main

import (
	"bytes"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"log"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"text/template"

	yaml "gopkg.in/yaml.v2"
	"mvdan.cc/gofumpt/format"
)

var tpl = `// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at http://mozilla.org/MPL/2.0/.
// DO NOT EDIT: this file is automatically generated by docgen
package {{ .Package }}
import (
	"github.com/projectdiscovery/yamldoc-go/encoder"
)
{{ $tick := "` + "`" + `" -}}
var (
	{{ range $struct := .Structs -}}
	{{ $struct.Name }}Doc encoder.Doc
	{{ end -}}
)
func init() {
	{{ range $struct := .Structs -}}
	{{ $docVar := printf "%v%v" $struct.Name "Doc" }}
	{{ $docVar }}.Type = "{{ $struct.Name }}"
	{{ $docVar }}.Comments[encoder.LineComment] = "{{ $struct.Text.Comment }}"
	{{ $docVar }}.Description = "{{ $struct.Text.Description }}"
	{{ range $example := $struct.Text.Examples }}
	{{ if $example.Value }}
	{{ $docVar }}.AddExample("{{ $example.Name }}", {{ $example.Value }})
	{{ end -}}
	{{ end -}}
	{{ if $struct.AppearsIn -}}
	{{ $docVar }}.AppearsIn = []encoder.Appearance{
	{{ range $value := $struct.AppearsIn -}}
		{
			TypeName: "{{ $value.Struct.Name }}",
			FieldName: "{{ $value.FieldName }}",
		},
	{{ end -}}
	}
	{{ end -}}
	{{ $docVar }}.Fields = make([]encoder.Doc,{{ len $struct.Fields }})
	{{ range $index, $field := $struct.Fields -}}
	{{ $docVar }}.Fields[{{ $index }}].Name = "{{ $field.Tag }}"
	{{ $docVar }}.Fields[{{ $index }}].Type = "{{ $field.Type }}"
	{{ $docVar }}.Fields[{{ $index }}].Note = "{{ $field.Note }}"
	{{ $docVar }}.Fields[{{ $index }}].Description = "{{ $field.Text.Description }}"
	{{ $docVar }}.Fields[{{ $index }}].Comments[encoder.LineComment] = "{{ $field.Text.Comment }}"
	{{ range $example := $field.Text.Examples }}
	{{ if $example.Value }}
	{{ $docVar }}.Fields[{{ $index }}].AddExample("{{ $example.Name }}", {{ $example.Value }})
	{{ end -}}
	{{ end -}}
	{{ if $field.Text.Values -}}
	{{ $docVar }}.Fields[{{ $index }}].Values = []string{
	{{ range $value := $field.Text.Values -}}
		"{{ $value }}",
	{{ end -}}
	}
	{{ end -}}
	{{ end -}}
	{{ end }}
}
{{ range $struct := .Structs -}}
func (_ {{ $struct.Name }}) Doc() *encoder.Doc {
	return &{{ $struct.Name }}Doc
}
{{ end -}}
// Get{{ .Name }}Doc returns documentation for the file {{ .File }}.
func Get{{ .Name }}Doc() *encoder.FileDoc {
	return &encoder.FileDoc{
		Name: "{{ .Name }}",
		Description: "{{ .Header }}",
		Structs: []*encoder.Doc{
			{{ range $struct := .Structs -}}
			&{{ $struct.Name }}Doc,
			{{ end -}}
		},
	}
}
`

type Doc struct {
	Name    string
	Package string
	Title   string
	Header  string
	File    string
	Structs []*Struct
}

type Struct struct {
	Name      string
	Text      *Text
	Fields    []*Field
	AppearsIn []Appearance
}

type Appearance struct {
	Struct    *Struct
	FieldName string
}

type Example struct {
	Name  string `yaml:"name"`
	Value string `yaml:"value"`
}

type Field struct {
	Name    string
	Type    string
	TypeRef string
	Text    *Text
	Tag     string
	Note    string

	embeddedStruct string
}

type Text struct {
	Comment     string     `json:"-"`
	Description string     `json:"description"`
	Examples    []*Example `json:"examples"`
	Values      []string   `json:"values"`
}

func in(p string) (string, error) {
	return filepath.Abs(p)
}

func out(p string) (*os.File, error) {
	abs, err := filepath.Abs(p)
	if err != nil {
		return nil, err
	}

	return os.Create(abs)
}

type structType struct {
	name string
	text *Text
	pos  token.Pos
	node *ast.StructType
}

func collectStructs(node ast.Node) []*structType {
	structs := []*structType{}

	collectStructs := func(n ast.Node) bool {
		g, ok := n.(*ast.GenDecl)
		if !ok {
			return true
		}

		if g.Doc != nil {
			for _, comment := range g.Doc.List {
				if strings.Contains(comment.Text, "docgen: nodoc") {
					return true
				}
			}
		}

		for _, spec := range g.Specs {
			t, ok := spec.(*ast.TypeSpec)
			if !ok {
				return true
			}

			if t.Type == nil {
				return true
			}

			x, ok := t.Type.(*ast.StructType)
			if !ok {
				return true
			}

			structName := t.Name.Name

			text := &Text{}

			if t.Doc != nil {
				text = parseComment([]byte(t.Doc.Text()))
			} else if g.Doc != nil {
				text = parseComment([]byte(g.Doc.Text()))
			}

			s := &structType{
				name: structName,
				text: text,
				node: x,
				pos:  x.Pos(),
			}

			structs = append(structs, s)
		}

		return true
	}

	ast.Inspect(node, collectStructs)

	return structs
}

func parseComment(comment []byte) *Text {
	text := &Text{}
	if err := yaml.Unmarshal(comment, text); err != nil {
		// not yaml, fallback
		text.Description = string(comment)
		// take only the first line from the Description for the comment
		text.Comment = strings.Split(text.Description, "\n")[0]

		// try to parse the everything except for the first line as yaml
		if err = yaml.Unmarshal([]byte(strings.Join(strings.Split(text.Description, "\n")[1:], "\n")), text); err == nil {
			// if parsed, remove it from the description
			text.Description = text.Comment
		}
	} else {
		text.Description = strings.TrimSpace(text.Description)
		// take only the first line from the Description for the comment
		text.Comment = strings.Split(text.Description, "\n")[0]
	}

	text.Description = escape(text.Description)
	for _, example := range text.Examples {
		example.Name = escape(example.Name)
		example.Value = strings.TrimSpace(example.Value)
	}

	return text
}

func getFieldType(p interface{}) string {
	if m, ok := p.(*ast.MapType); ok {
		return getFieldType(m.Value)
	}

	switch t := p.(type) {
	case *ast.Ident:
		return t.Name
	case *ast.ArrayType:
		return getFieldType(p.(*ast.ArrayType).Elt)
	case *ast.StarExpr:
		return getFieldType(t.X)
	case *ast.SelectorExpr:
		return getFieldType(t.Sel)
	default:
		return ""
	}
}

func formatFieldType(p interface{}) string {
	if m, ok := p.(*ast.MapType); ok {
		return fmt.Sprintf("map[%s]%s", formatFieldType(m.Key), formatFieldType(m.Value))
	}

	switch t := p.(type) {
	case *ast.Ident:
		return t.Name
	case *ast.ArrayType:
		return "[]" + formatFieldType(p.(*ast.ArrayType).Elt)
	case *ast.StructType:
		return "struct"
	case *ast.StarExpr:
		return formatFieldType(t.X)
	case *ast.SelectorExpr:
		return formatFieldType(t.Sel)
	case *ast.InterfaceType:
		return "interface{}"
	default:
		log.Printf("unknown: %#v", t)
		return ""
	}
}

func escape(value string) string {
	return strings.TrimSpace(strings.ReplaceAll(
		strings.ReplaceAll(value, "\"", "\\\""),
		"\n",
		"\\n",
	))
}

func collectFields(s *structType) (fields []*Field) {
	fields = []*Field{}

	for _, f := range s.node.Fields.List {
		if f.Doc == nil {
			log.Fatalf("field %q is missing a documentation", f.Names[0].Name)
		}

		if strings.Contains(f.Doc.Text(), "docgen:nodoc") {
			continue
		}

		if len(f.Names) == 0 {
			gotStruct, ok := f.Type.(*ast.Ident)
			if !ok {
				continue
			}
			typeSpec, ok := gotStruct.Obj.Decl.(*ast.TypeSpec)
			if !ok {
				continue
			}
			structData := typeSpec.Type.(*ast.StructType)
			if !ok {
				continue
			}
			log.Printf("got embedded struct: %+v\n", structData)

			embeddedFields := collectFields(&structType{node: structData})
			for _, field := range embeddedFields {
				fields = append(fields, field)
			}
			continue
		}
		name := f.Names[0].Name

		fieldType := formatFieldType(f.Type)
		fieldTypeRef := getFieldType(f.Type)

		tag := reflect.StructTag(strings.Trim(f.Tag.Value, "`"))
		yamlTag := tag.Get("yaml")
		yamlTag = strings.Split(yamlTag, ",")[0]

		if yamlTag == "" {
			yamlTag = strings.ToLower(yamlTag)
		}

		text := parseComment([]byte(f.Doc.Text()))

		field := &Field{
			Name:    name,
			Tag:     yamlTag,
			Type:    fieldType,
			TypeRef: fieldTypeRef,
			Text:    text,
		}

		if f.Comment != nil {
			field.Note = escape(f.Comment.Text())
		}

		fields = append(fields, field)
	}

	return fields
}

func render(doc *Doc, dest string) {
	t := template.Must(template.New("docfile.tpl").Parse(tpl))
	buf := bytes.Buffer{}

	err := t.Execute(&buf, doc)
	if err != nil {
		panic(err)
	}

	formatted, err := format.Source(buf.Bytes(), format.Options{})
	if err != nil {
		log.Printf("data: %s", buf.Bytes())
		panic(err)
	}

	out, err := out(dest)
	defer out.Close()
	_, err = out.Write(formatted)

	if err != nil {
		panic(err)
	}
}

func main() {
	abs, err := in(os.Args[1])
	if err != nil {
		log.Fatal(err)
	}

	fmt.Printf("creating package file set: %q\n", abs)

	fset := token.NewFileSet()

	node, err := parser.ParseFile(fset, abs, nil, parser.ParseComments)
	if err != nil {
		log.Fatal(err)
	}

	var structs []*structType

	packageName := node.Name.Name

	tokenFile := fset.File(node.Pos())
	if tokenFile == nil {
		log.Fatalf("No token")
	}

	fmt.Printf("parsing file in package %q: %s\n", packageName, tokenFile.Name())

	structs = append(structs, collectStructs(node)...)

	if len(structs) == 0 {
		log.Fatalf("failed to find types that could be documented in %s", abs)
	}

	doc := &Doc{
		Package: packageName,
		Structs: []*Struct{},
	}

	extraExamples := map[string][]*Example{}
	backReferences := map[string][]Appearance{}

	for _, s := range structs {
		fmt.Printf("generating docs for type: %q\n", s.name)

		fields := collectFields(s)

		s := &Struct{
			Name:   s.name,
			Text:   s.text,
			Fields: fields,
		}

		for _, field := range fields {
			if field.TypeRef == "" {
				continue
			}

			if len(field.Text.Examples) > 0 {
				extraExamples[field.TypeRef] = append(extraExamples[field.TypeRef], field.Text.Examples...)
			}

			backReferences[field.TypeRef] = append(backReferences[field.TypeRef], Appearance{
				Struct:    s,
				FieldName: field.Tag,
			})
		}

		doc.Structs = append(doc.Structs, s)
	}

	for _, s := range doc.Structs {
		if extra, ok := extraExamples[s.Name]; ok {
			s.Text.Examples = append(s.Text.Examples, extra...)
		}

		if ref, ok := backReferences[s.Name]; ok {
			s.AppearsIn = append(s.AppearsIn, ref...)
		}
	}

	if len(os.Args) != 4 {
		log.Fatalf("expected 3 args, got %d", len(os.Args)-1)
	}

	if err == nil {
		doc.Package = node.Name.Name
		doc.Name = os.Args[3]

		if node.Doc != nil {
			doc.Header = escape(node.Doc.Text())
		}
	}

	doc.File = os.Args[2]
	render(doc, os.Args[2])
}
