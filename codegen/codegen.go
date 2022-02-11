package codegen

import (
	"bufio"
	"context"
	"fmt"
	"reflect"
	"strconv"
	"strings"

	"github.com/lithammer/dedent"
	"github.com/moby/buildkit/client"
	"github.com/openllb/hlb/checker"
	"github.com/openllb/hlb/errdefs"
	"github.com/openllb/hlb/linter"
	"github.com/openllb/hlb/parser"
	"github.com/openllb/hlb/parser/ast"
	"github.com/openllb/hlb/solver"
	"github.com/pkg/errors"
)

type CodeGen struct {
	Debug          Debugger
	cln            *client.Client
	resolver       Resolver
	extraSolveOpts []solver.SolveOption
}

type CodeGenOption func(*CodeGen) error

func WithDebugger(dbgr Debugger) CodeGenOption {
	return func(i *CodeGen) error {
		i.Debug = dbgr
		return nil
	}
}

func WithExtraSolveOpts(extraOpts []solver.SolveOption) CodeGenOption {
	return func(i *CodeGen) error {
		i.extraSolveOpts = append(i.extraSolveOpts, extraOpts...)
		return nil
	}
}

func New(cln *client.Client, resolver Resolver, opts ...CodeGenOption) (*CodeGen, error) {
	cg := &CodeGen{
		Debug:    NewNoopDebugger(),
		cln:      cln,
		resolver: resolver,
	}
	for _, opt := range opts {
		err := opt(cg)
		if err != nil {
			return cg, err
		}
	}

	return cg, nil
}

type Target struct {
	Name string
}

func (cg *CodeGen) Generate(ctx context.Context, mod *ast.Module, targets []Target) (solver.Request, error) {
	var requests []solver.Request

	for i, target := range targets {
		_, ok := mod.Scope.Objects[target.Name]
		if !ok {
			return nil, fmt.Errorf("target %q is not defined in %s", target.Name, mod.Pos.Filename)
		}

		// Yield before compiling anything.
		ret := NewRegister(ctx)
		err := cg.Debug(ctx, mod.Scope, mod, ret, nil)
		if err != nil {
			return nil, err
		}

		// Build expression for target.
		ie := ast.NewIdentExpr(target.Name)
		ie.Pos.Filename = "target"
		ie.Pos.Line = i

		// Every target has a return register.
		err = cg.EmitIdentExpr(ctx, mod.Scope, ie, ie.Ident, nil, nil, nil, ret)
		if err != nil {
			return nil, err
		}

		request, err := ret.Value().Request()
		if err != nil {
			return nil, err
		}

		requests = append(requests, request)
	}

	return solver.Parallel(requests...), nil
}

func (cg *CodeGen) EmitExpr(ctx context.Context, scope *ast.Scope, expr *ast.Expr, args []Value, opts Option, b *ast.Binding, ret Register) error {
	ctx = WithProgramCounter(ctx, expr)

	switch {
	case expr.FuncLit != nil:
		return cg.EmitFuncLit(ctx, scope, expr.FuncLit, b, ret)
	case expr.BasicLit != nil:
		return cg.EmitBasicLit(ctx, scope, expr.BasicLit, ret)
	case expr.CallExpr != nil:
		return ret.SetAsync(func(v Value) (Value, error) {
			err := cg.lookupCall(ctx, scope, expr.CallExpr.Name.Ident)
			if err != nil {
				return nil, err
			}

			ret := NewRegister(ctx)
			ret.Set(v)
			err = cg.EmitCallExpr(ctx, scope, expr.CallExpr, ret)
			return ret.Value(), err
		})
	default:
		return errdefs.WithInternalErrorf(expr, "invalid expr")
	}
}

func (cg *CodeGen) EmitFuncLit(ctx context.Context, scope *ast.Scope, lit *ast.FuncLit, b *ast.Binding, ret Register) error {
	return cg.EmitBlock(ctx, scope, lit.Body, b, ret)
}

func (cg *CodeGen) EmitBasicLit(ctx context.Context, scope *ast.Scope, lit *ast.BasicLit, ret Register) error {
	switch {
	case lit.Decimal != nil:
		return ret.Set(*lit.Decimal)
	case lit.Numeric != nil:
		return ret.Set(int(lit.Numeric.Value))
	case lit.Bool != nil:
		return ret.Set(*lit.Bool)
	case lit.Str != nil:
		return cg.EmitStringLit(ctx, scope, lit.Str, ret)
	case lit.RawString != nil:
		return ret.Set(lit.RawString.Text)
	case lit.Heredoc != nil:
		return cg.EmitHeredoc(ctx, scope, lit.Heredoc, ret)
	case lit.RawHeredoc != nil:
		return cg.EmitRawHeredoc(ctx, scope, lit.RawHeredoc, ret)
	default:
		return errdefs.WithInternalErrorf(lit, "invalid basic lit")
	}
}

func (cg *CodeGen) EmitStringLit(ctx context.Context, scope *ast.Scope, str *ast.StringLit, ret Register) error {
	var pieces []string
	for _, f := range str.Fragments {
		switch {
		case f.Escaped != nil:
			escaped := *f.Escaped
			if escaped[1] == '$' {
				pieces = append(pieces, "$")
			} else {
				value, _, _, err := strconv.UnquoteChar(escaped, '"')
				if err != nil {
					return err
				}
				pieces = append(pieces, string(value))
			}
		case f.Interpolated != nil:
			exprRet := NewRegister(ctx)
			err := cg.EmitExpr(ctx, scope, f.Interpolated.Expr, nil, nil, nil, exprRet)
			if err != nil {
				return err
			}

			piece, err := exprRet.Value().String()
			if err != nil {
				return err
			}

			pieces = append(pieces, piece)
		case f.Text != nil:
			pieces = append(pieces, *f.Text)
		}
	}
	return ret.Set(strings.Join(pieces, ""))
}

func (cg *CodeGen) EmitHeredoc(ctx context.Context, scope *ast.Scope, heredoc *ast.Heredoc, ret Register) error {
	var pieces []string
	for _, f := range heredoc.Fragments {
		switch {
		case f.Spaces != nil:
			pieces = append(pieces, *f.Spaces)
		case f.Escaped != nil:
			escaped := *f.Escaped
			if escaped[1] == '$' {
				pieces = append(pieces, "$")
			} else {
				pieces = append(pieces, escaped)
			}
		case f.Interpolated != nil:
			exprRet := NewRegister(ctx)
			err := cg.EmitExpr(ctx, scope, f.Interpolated.Expr, nil, nil, nil, exprRet)
			if err != nil {
				return err
			}

			piece, err := exprRet.Value().String()
			if err != nil {
				return err
			}

			pieces = append(pieces, piece)
		case f.Text != nil:
			pieces = append(pieces, *f.Text)
		}
	}
	return emitHeredocPieces(heredoc.Start, heredoc.Terminate.Text, pieces, ret)
}

func emitHeredocPieces(start, terminate string, pieces []string, ret Register) error {
	// Build raw heredoc.
	raw := strings.Join(pieces, "")

	// Trim leading newlines and trailing newlines / tabs.
	raw = strings.TrimRight(strings.TrimLeft(raw, "\n"), "\n\t")

	switch strings.TrimSuffix(start, terminate) {
	case "<<-": // dedent
		return ret.Set(dedent.Dedent(raw))
	case "<<~": // fold
		s := bufio.NewScanner(strings.NewReader(strings.TrimSpace(raw)))
		var lines []string
		for s.Scan() {
			lines = append(lines, strings.TrimSpace(s.Text()))
		}
		return ret.Set(strings.Join(lines, " "))
	default:
		return ret.Set(raw)
	}
}

func (cg *CodeGen) EmitRawHeredoc(ctx context.Context, scope *ast.Scope, heredoc *ast.RawHeredoc, ret Register) error {
	var pieces []string
	for _, f := range heredoc.Fragments {
		switch {
		case f.Spaces != nil:
			pieces = append(pieces, *f.Spaces)
		case f.Text != nil:
			pieces = append(pieces, *f.Text)
		}
	}

	terminate := fmt.Sprintf("`%s`", heredoc.Terminate.Text)
	return emitHeredocPieces(heredoc.Start, terminate, pieces, ret)
}

func (cg *CodeGen) EmitCallExpr(ctx context.Context, scope *ast.Scope, call *ast.CallExpr, ret Register) error {
	ctx = WithFrame(ctx, Frame{call.Name})

	// Yield before executing call expression.
	err := cg.Debug(ctx, scope, call.Name, ret, nil)
	if err != nil {
		return err
	}

	args, err := cg.Evaluate(ctx, scope, nil, call.Signature, call.Args()...)
	if err != nil {
		return err
	}
	for i, arg := range call.Args() {
		ctx = WithArg(ctx, i, arg)
	}

	return cg.EmitIdentExpr(ctx, scope, call.Name, call.Name.Ident, args, nil, nil, ret)
}

func (cg *CodeGen) EmitIdentExpr(ctx context.Context, scope *ast.Scope, ie *ast.IdentExpr, lookup *ast.Ident, args []Value, opts Option, b *ast.Binding, ret Register) error {
	ctx = WithProgramCounter(ctx, ie)

	obj := scope.Lookup(lookup.Text)
	if obj == nil {
		return errors.WithStack(errdefs.WithUndefinedIdent(lookup, nil))
	}

	switch n := obj.Node.(type) {
	case *ast.BuiltinDecl:
		return ret.SetAsync(func(v Value) (Value, error) {
			return cg.EmitBuiltinDecl(ctx, scope, n, args, opts, b, v)
		})
	case *ast.FuncDecl:
		return cg.EmitFuncDecl(ctx, n, args, nil, ret)
	case *ast.BindClause:
		return cg.EmitBinding(ctx, n.TargetBinding(lookup.Text), args, ret)
	case *ast.ImportDecl:
		imod, ok := obj.Data.(*ast.Module)
		if !ok {
			return errdefs.WithInternalErrorf(ProgramCounter(ctx), "expected imported module to be resolved")
		}
		return cg.EmitIdentExpr(ctx, imod.Scope, ie, ie.Reference.Ident, args, opts, nil, ret)
	case *ast.Field:
		val, err := NewValue(ctx, obj.Data)
		if err != nil {
			return err
		}
		if val.Kind() != ast.Option || ret.Value().Kind() != ast.Option {
			return ret.Set(val)
		} else {
			retOpts, err := ret.Value().Option()
			if err != nil {
				return err
			}
			valOpts, err := val.Option()
			if err != nil {
				return err
			}
			return ret.Set(append(retOpts, valOpts...))
		}
	default:
		return errdefs.WithInternalErrorf(n, "invalid resolved object")
	}
}

func (cg *CodeGen) EmitImport(ctx context.Context, mod *ast.Module, id *ast.ImportDecl) (imod *ast.Module, filename string, err error) {
	// Import expression can be string or fs.
	ctx = WithReturnType(ctx, ast.None)

	ret := NewRegister(ctx)
	err = cg.EmitExpr(ctx, mod.Scope, id.Expr, nil, nil, nil, ret)
	if err != nil {
		return
	}
	val := ret.Value()

	dir := mod.Directory
	switch val.Kind() {
	case ast.Filesystem:
		var fs Filesystem
		fs, err = val.Filesystem()
		if err != nil {
			return
		}

		filename = ModuleFilename
		dir, err = cg.resolver.Resolve(ctx, id, fs)
		if err != nil {
			return
		}
	case ast.String:
		filename, err = val.String()
		if err != nil {
			return
		}
	}

	rc, err := dir.Open(filename)
	if err != nil {
		if !errdefs.IsNotExist(err) {
			return
		}
		if id.DeprecatedPath != nil {
			err = errdefs.WithImportPathNotExist(err, id.DeprecatedPath, filename)
			return
		}
		if id.Expr.FuncLit != nil {
			err = errdefs.WithImportPathNotExist(err, id.Expr.FuncLit.Type, filename)
			return
		}
		err = errdefs.WithImportPathNotExist(err, id.Expr, filename)
		return
	}
	defer rc.Close()

	imod, err = parser.Parse(ctx, rc)
	if err != nil {
		return
	}
	imod.Directory = dir

	err = checker.SemanticPass(imod)
	if err != nil {
		return
	}

	// Drop errors from linting.
	_ = linter.Lint(ctx, imod)

	err = checker.Check(imod)
	return
}

func (cg *CodeGen) EmitBuiltinDecl(ctx context.Context, scope *ast.Scope, bd *ast.BuiltinDecl, args []Value, opts Option, b *ast.Binding, v Value) (Value, error) {
	var callable interface{}
	if ReturnType(ctx) != ast.None {
		callable = Callables[ReturnType(ctx)][bd.Name]
	} else {
		for _, kind := range bd.Kinds {
			c, ok := Callables[kind][bd.Name]
			if ok {
				callable = c
				break
			}
		}
	}
	if callable == nil {
		return nil, errdefs.WithInternalErrorf(ProgramCounter(ctx), "unrecognized builtin `%s`", bd)
	}

	// Pass binding if available.
	if b != nil {
		ctx = WithBinding(ctx, b)
	}

	for _, opt := range cg.extraSolveOpts {
		opts = append(opts, opt)
	}

	var (
		c   = reflect.ValueOf(callable).MethodByName("Call")
		ins = []reflect.Value{
			reflect.ValueOf(ctx),
			reflect.ValueOf(cg.cln),
			reflect.ValueOf(v),
			reflect.ValueOf(opts),
		}
	)

	// Handle variadic arguments separately.
	numIn := c.Type().NumIn()
	if c.Type().IsVariadic() {
		numIn -= 1
	}

	expected := numIn - len(PrototypeIn)
	if len(args) < expected {
		return nil, errdefs.WithInternalErrorf(ProgramCounter(ctx), "`%s` expected %d args, got %d", bd, expected, len(args))
	}

	// Reflect regular arguments.
	for i := len(PrototypeIn); i < numIn; i++ {
		var (
			param = c.Type().In(i)
			arg   = args[i-len(PrototypeIn)]
		)
		v, err := arg.Reflect(param)
		if err != nil {
			return nil, err
		}
		ins = append(ins, v)
	}

	// Reflect variadic arguments.
	if c.Type().IsVariadic() {
		for i := numIn - len(PrototypeIn); i < len(args); i++ {
			param := c.Type().In(numIn).Elem()
			v, err := args[i].Reflect(param)
			if err != nil {
				return nil, err
			}
			ins = append(ins, v)
		}
	}

	outs := c.Call(ins)
	if !outs[1].IsNil() {
		return nil, WithBacktraceError(ctx, outs[1].Interface().(error))
	}
	return outs[0].Interface().(Value), nil
}

func (cg *CodeGen) EmitFuncDecl(ctx context.Context, fun *ast.FuncDecl, args []Value, b *ast.Binding, ret Register) error {
	ctx = WithProgramCounter(ctx, fun.Name)

	params := fun.Params.Fields()
	if len(params) != len(args) {
		name := fun.Name.Text
		if b != nil {
			name = b.Name.Text
		}
		return errdefs.WithInternalErrorf(ProgramCounter(ctx), "`%s` expected %d args, got %d", name, len(params), len(args))
	}

	scope := ast.NewScope(fun, fun.Scope)
	for i, param := range params {
		if param.Modifier != nil {
			continue
		}

		scope.Insert(&ast.Object{
			Kind:  param.Kind(),
			Ident: param.Name,
			Node:  param,
			Data:  args[i],
		})
	}

	// Yield before executing a function.
	err := cg.Debug(ctx, scope, fun.Name, ret, nil)
	if err != nil {
		return err
	}

	return cg.EmitBlock(ctx, scope, fun.Body, b, ret)
}

func (cg *CodeGen) EmitBinding(ctx context.Context, b *ast.Binding, args []Value, ret Register) error {
	return cg.EmitFuncDecl(ctx, b.Bind.Closure, args, b, ret)
}

func (cg *CodeGen) lookupCall(ctx context.Context, scope *ast.Scope, lookup *ast.Ident) error {
	obj := scope.Lookup(lookup.Text)
	if obj == nil {
		return errors.WithStack(errdefs.WithUndefinedIdent(lookup, nil))
	}

	switch n := obj.Node.(type) {
	case *ast.ImportDecl:
		_, ok := obj.Data.(*ast.Module)
		if ok {
			break
		}

		mod := scope.Root().Node.(*ast.Module)
		imod, _, err := cg.EmitImport(ctx, mod, n)
		if err != nil {
			return err
		}
		obj.Data = imod

		err = checker.CheckReferences(mod, n.Name.Text)
		if err != nil {
			return err
		}
	}

	return nil
}

func (cg *CodeGen) EmitBlock(ctx context.Context, scope *ast.Scope, block *ast.BlockStmt, b *ast.Binding, ret Register) error {
	if block == nil {
		return nil
	}

	ctx = WithReturnType(ctx, block.Kind())
	for _, stmt := range block.Stmts() {
		stmt := stmt
		var err error
		switch {
		case stmt.Call != nil:
			err = ret.SetAsync(func(v Value) (Value, error) {
				err := cg.lookupCall(ctx, scope, stmt.Call.Name.Ident)
				if err != nil {
					return nil, err
				}

				ret := NewRegister(ctx)
				ret.Set(v)
				err = cg.EmitCallStmt(ctx, scope, stmt.Call, b, ret)
				return ret.Value(), err
			})
		case stmt.Expr != nil:
			err = cg.EmitExpr(ctx, scope, stmt.Expr.Expr, nil, nil, b, ret)
		default:
			return errdefs.WithInternalErrorf(stmt, "invalid stmt")
		}
		if err != nil {
			return err
		}
	}

	return nil
}

func (cg *CodeGen) EmitCallStmt(ctx context.Context, scope *ast.Scope, call *ast.CallStmt, b *ast.Binding, ret Register) error {
	ctx = WithFrame(ctx, Frame{call.Name})

	args, err := cg.Evaluate(ctx, scope, nil, call.Signature, call.Args...)
	if err != nil {
		return err
	}
	for i, arg := range call.Args {
		ctx = WithArg(ctx, i, arg)
	}

	var opts Option
	if call.WithClause != nil {
		// Provide a type hint to avoid ambgiuous lookups.
		kinds := []ast.Kind{
			ast.Kind(fmt.Sprintf("%s::%s", ast.Option, call.Name)),
		}

		// WithClause provides option expressions access to the binding.
		values, err := cg.Evaluate(ctx, scope, b, kinds, call.WithClause.Expr)
		if err != nil {
			return err
		}

		opts, err = values[0].Option()
		if err != nil {
			return err
		}
	}

	// Yield before executing the next call statement.
	if call.Breakpoint(ReturnType(ctx)) {
		var command []string
		for _, arg := range args {
			if arg.Kind() != ast.String {
				return errors.New("breakpoint args must be strings")
			}
			argStr, err := arg.String()
			if err != nil {
				return err
			}
			command = append(command, argStr)
		}
		if len(command) != 0 {
			opts = append(opts, breakpointCommand(command))
		}
	}
	err = cg.Debug(ctx, scope, call.Name, ret, opts)
	if err != nil {
		return err
	}

	// Pass the binding if this is the matching CallStmt.
	var binding *ast.Binding
	if b != nil && call.BindClause == b.Bind {
		binding = b
	}

	return cg.EmitIdentExpr(ctx, scope, call.Name, call.Name.Ident, args, opts, binding, ret)
}

type breakpointCommand []string

func (cg *CodeGen) Evaluate(ctx context.Context, scope *ast.Scope, b *ast.Binding, kinds []ast.Kind, exprs ...*ast.Expr) (values []Value, err error) {
	if len(kinds) != len(exprs) {
		return nil, errdefs.WithInternalErrorf(ProgramCounter(ctx), "expected %d kinds but got %d", len(exprs), len(kinds))
	}
	for i, expr := range exprs {
		ctx = WithProgramCounter(ctx, expr)
		ctx = WithReturnType(ctx, kinds[i])

		// Evaluated expressions write to a new return register.
		ret := NewRegister(ctx)
		err = cg.EmitExpr(ctx, scope, expr, nil, nil, b, ret)
		if err != nil {
			return
		}
		values = append(values, ret.Value())
	}
	return
}
