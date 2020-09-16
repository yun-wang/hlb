//go:generate go run ../../cmd/builtingen ../../language/builtin.hlb ../lookup.go

package gen

import (
	"bytes"
	"context"
	"fmt"
	"go/format"
	"html/template"
	"io"
	"log"
	"os"
	"strconv"
	"strings"

	"github.com/openllb/hlb/diagnostic"
	"github.com/openllb/hlb/parser"
	"github.com/openllb/hlb/pkg/filebuffer"
)

type BuiltinData struct {
	Command     string
	FuncsByKind map[parser.Kind][]ParsedFunc
	Reference   string
}

type ParsedFunc struct {
	Name    string
	Params  []*parser.Field
	Effects []*parser.Field
}

func GenerateBuiltins(ctx context.Context, r io.Reader) ([]byte, error) {
	sources := filebuffer.NewSources()
	ctx = diagnostic.WithSources(ctx, sources)
	mod, err := parser.Parse(ctx, r)
	if err != nil {
		return nil, err
	}

	funcsByKind := make(map[parser.Kind][]ParsedFunc)
	for _, decl := range mod.Decls {
		fun := decl.Func
		if fun == nil {
			continue
		}

		var effects []*parser.Field
		if fun.Effects != nil && fun.Effects.Effects != nil {
			effects = fun.Effects.Effects.Fields()
		}

		kind := fun.Type.Kind
		funcsByKind[kind] = append(funcsByKind[kind], ParsedFunc{
			Name:    fun.Name.Text,
			Params:  fun.Params.Fields(),
			Effects: effects,
		})
	}

	fb := sources.Get(mod.Pos.Filename)
	data := BuiltinData{
		Command:     fmt.Sprintf("builtingen %s", strings.Join(os.Args[1:], " ")),
		FuncsByKind: funcsByKind,
		Reference:   fmt.Sprintf("`%s`", string(fb.Bytes())),
	}

	var buf bytes.Buffer
	err = referenceTmpl.Execute(&buf, &data)
	if err != nil {
		return nil, err
	}

	src, err := format.Source(buf.Bytes())
	if err != nil {
		log.Printf("warning: internal error: invalid Go generated: %s", err)
		log.Printf("warning: compile the package to analyze the error")
		src = buf.Bytes()
	}

	return src, nil
}

var tmplFunctions = template.FuncMap{
	"kind": func(kind parser.Kind) template.HTML {
		switch kind {
		case parser.String:
			return template.HTML("parser.String")
		case parser.Int:
			return template.HTML("parser.Int")
		case parser.Bool:
			return template.HTML("parser.Bool")
		case parser.Filesystem:
			return template.HTML("parser.Filesystem")
		default:
			return template.HTML(strconv.Quote(string(kind)))
		}
	},
}

var referenceTmpl = template.Must(template.New("reference").Funcs(tmplFunctions).Parse(`
// Code generated by {{.Command}}; DO NOT EDIT.

package builtin

import "github.com/openllb/hlb/parser"

type BuiltinLookup struct {
	ByKind map[parser.Kind]LookupByKind
}

type LookupByKind struct {
	 Func map[string]FuncLookup
}

type FuncLookup struct {
	Params []*parser.Field
	Effects []*parser.Field
}

var (
	Lookup = BuiltinLookup{
		ByKind: map[parser.Kind]LookupByKind{
			{{range $kind, $funcs := .FuncsByKind}}{{kind $kind}}: LookupByKind{
				Func: map[string]FuncLookup{
					{{range $i, $func := $funcs}}"{{$func.Name}}": FuncLookup{
						Params:  []*parser.Field{
							{{range $i, $param := $func.Params}}parser.NewField({{kind $param.Type.Kind}}, "{{$param.Name}}", {{if $param.Modifier}}true{{else}}false{{end}}),
							{{end}}
						},
						Effects: []*parser.Field{
							{{range $i, $effect := $func.Effects}}parser.NewField({{kind $effect.Type.Kind}}, "{{$effect.Name}}", false),
							{{end}}
						},
					},
					{{end}}
				},
			},
			{{end}}
		},
	}

	Reference = {{.Reference}}
)
`))
