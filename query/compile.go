package query

import (
	"errors"
	"fmt"

	"github.com/influxdata/influxdb/influxql"
)

type compiledStatement struct {
	// Sources holds the data sources this will query from.
	Sources influxql.Sources

	// FunctionCalls holds a reference to all of the function calls that have
	// been instantiated.
	FunctionCalls []*FunctionCall

	// AuxFields holds a mapping to the auxiliary fields that need to be
	// selected. This maps the raw VarRef to a pointer to a shared VarRef. The
	// pointer is used for instantiating references to the shared variable so
	// type mapping gets shared.
	AuxiliaryFields *AuxiliaryFields

	// Distinct holds the Distinct statement. If Distinct is set, there can be
	// no auxiliary fields or function calls.
	Distinct *Distinct

	// OutputEdges holds the outermost edges that will be used to read from
	// when returning results.
	OutputEdges []*OutputEdge
}

type CompiledStatement interface {
	Select(plan *Plan) ([]*OutputEdge, error)
}

func newCompiler(stmt *influxql.SelectStatement) *compiledStatement {
	return &compiledStatement{
		FunctionCalls: make([]*FunctionCall, 0, 5),
		OutputEdges:   make([]*OutputEdge, 0, len(stmt.Fields)),
	}
}

func (c *compiledStatement) compileExpr(expr influxql.Expr) (*OutputEdge, error) {
	switch expr := expr.(type) {
	case *influxql.VarRef:
		// If there is no instance of AuxiliaryFields, instantiate one now.
		if c.AuxiliaryFields == nil {
			c.AuxiliaryFields = &AuxiliaryFields{}
		}
		return c.AuxiliaryFields.Iterator(expr), nil
	case *influxql.Call:
		switch expr.Name {
		case "count", "min", "max", "sum", "first", "last", "mean":
			return c.compileFunction(expr)
		default:
			return nil, errors.New("unimplemented")
		}
	case *influxql.BinaryExpr:
		// Check if either side is a literal so we only compile one side if it is.
		if _, ok := expr.LHS.(influxql.Literal); ok {
		} else if _, ok := expr.RHS.(influxql.Literal); ok {
		} else {
			lhs, err := c.compileExpr(expr.LHS)
			if err != nil {
				return nil, err
			}
			rhs, err := c.compileExpr(expr.RHS)
			if err != nil {
				return nil, err
			}
			node := &BinaryExpr{LHS: lhs, RHS: rhs, Op: expr.Op}
			lhs.Node, rhs.Node = node, node

			var out *OutputEdge
			node.Output, out = NewEdge(node)
			return out, nil
		}
	}
	return nil, errors.New("unimplemented")
}

func (c *compiledStatement) compileFunction(expr *influxql.Call) (*OutputEdge, error) {
	if exp, got := 1, len(expr.Args); exp != got {
		return nil, fmt.Errorf("invalid number of arguments for %s, expected %d, got %d", expr.Name, exp, got)
	}

	// If we have count(), the argument may be a distinct() call.
	if expr.Name == "count" {
		if arg0, ok := expr.Args[0].(*influxql.Call); ok && arg0.Name == "distinct" {
			return nil, errors.New("unimplemented")
		}
	}

	// Must be a variable reference.
	arg0, ok := expr.Args[0].(*influxql.VarRef)
	if !ok {
		return nil, fmt.Errorf("expected field argument in %s()", expr.Name)
	}
	call := &FunctionCall{
		Name: expr.Name,
		Arg:  *arg0,
	}
	c.FunctionCalls = append(c.FunctionCalls, call)

	var out *OutputEdge
	call.Output, out = NewEdge(call)
	return out, nil
}

func (c *compiledStatement) validateFields() error {
	if c.AuxiliaryFields == nil && len(c.FunctionCalls) == 0 {
		return errors.New("at least 1 non-time field must be queried")
	}

	if c.AuxiliaryFields != nil {
		onlySelectors := true
		for _, call := range c.FunctionCalls {
			switch call.Name {
			case "top", "buttom", "max", "min", "first", "last", "percentile", "sample":
			default:
				onlySelectors = false
				break
			}
		}

		if !onlySelectors {
			return fmt.Errorf("mixing aggregate and non-aggregate queries is not supported")
		} else if len(c.FunctionCalls) > 1 {
			return fmt.Errorf("mixing multiple selector functions with tags or fields is not supported")
		}
	}

	if len(c.FunctionCalls) > 1 {
		for _, call := range c.FunctionCalls {
			switch call.Name {
			case "top", "bottom":
				// Ensure there are not multiple calls if top/bottom is present.
				if len(c.FunctionCalls) > 1 {
					return fmt.Errorf("selector function %s() cannot be combined with other functions", call.Name)
				}
			}
		}
	}
	return nil
}

func Compile(stmt *influxql.SelectStatement) (CompiledStatement, error) {
	// Compile each of the expressions.
	c := newCompiler(stmt)
	c.Sources = append(c.Sources, stmt.Sources...)
	for _, f := range stmt.Fields {
		if ref, ok := f.Expr.(*influxql.VarRef); ok && ref.Val == "time" {
			continue
		}

		out, err := c.compileExpr(f.Expr)
		if err != nil {
			return nil, err
		}
		c.OutputEdges = append(c.OutputEdges, out)
	}

	if err := c.validateFields(); err != nil {
		return nil, err
	}
	return c, nil
}

func (c *compiledStatement) Select(plan *Plan) ([]*OutputEdge, error) {
	// If we have functions, instantiate all of them here.
	if len(c.FunctionCalls) > 0 {
		for _, call := range c.FunctionCalls {
			merge := &Merge{}
			for _, source := range c.Sources {
				switch source := source.(type) {
				case *influxql.Measurement:
					var auxFields []influxql.VarRef
					if c.AuxiliaryFields != nil {
						auxFields = c.AuxiliaryFields.Aux
					}
					ic := &IteratorCreator{
						Expr:        &call.Arg,
						Aux:         auxFields,
						Measurement: source,
					}
					ic.Output = merge.AddInput(ic)
				}
			}
			merge.Output, call.Input = AddEdge(merge, call)
			if c.AuxiliaryFields != nil {
				// If there are auxiliary fields, we need to insert it between
				// the call iterator and its output.
				c.AuxiliaryFields.Input, c.AuxiliaryFields.Output = call.Output.Insert(c.AuxiliaryFields)
			}
		}
	} else {
		// Instantiate IteratorCreator nodes for the auxiliary nodes.
		merge := &Merge{}
		for _, source := range c.Sources {
			switch source := source.(type) {
			case *influxql.Measurement:
				var auxFields []influxql.VarRef
				if c.AuxiliaryFields != nil {
					auxFields = c.AuxiliaryFields.Aux
				}
				ic := &IteratorCreator{Aux: auxFields, Measurement: source}
				ic.Output = merge.AddInput(ic)
			default:
				panic("unimplemented")
			}
		}
		merge.Output, c.AuxiliaryFields.Input = AddEdge(merge, c.AuxiliaryFields)
	}

	for _, out := range c.OutputEdges {
		plan.AddTarget(out)
	}
	return c.OutputEdges, nil
}

//func buildAuxIterators(stmt *influxql.SelectStatement, info *selectInfo) ([]*OutputEdge, error) {
//	// Determine auxiliary fields to be selected.
//	auxFields := make([]influxql.VarRef, 0, len(info.refs))
//	for ref := range info.refs {
//		auxFields = append(auxFields, *ref)
//	}
//	sort.Sort(influxql.VarRefs(auxFields))
//
//	// Create IteratorCreator node.
//	merge := &Merge{}
//	for _, source := range stmt.Sources {
//		switch source := source.(type) {
//		case *influxql.Measurement:
//			ic := &IteratorCreator{Aux: auxFields, Measurement: source}
//			ic.Output = merge.AddInput(ic)
//		default:
//			panic("unimplemented")
//		}
//	}
//
//	var out *OutputEdge
//	merge.Output, out = NewEdge(merge)
//
//	if stmt.Limit > 0 || stmt.Offset > 0 {
//		limit := &Limit{
//			Limit:  stmt.Limit,
//			Offset: stmt.Offset,
//			Input:  out,
//		}
//		limit.Output, out = limit.Input.Append(limit)
//	}
//
//	fields := &AuxiliaryFields{Aux: auxFields, Input: out}
//	out.Node = fields
//	outputs := make([]*OutputEdge, 0, len(stmt.Fields))
//	for _, field := range stmt.Fields {
//		outputs = append(outputs, buildAuxIterator(field.Expr, fields))
//	}
//	return outputs, nil
//}
//
//func buildAuxIterator(expr influxql.Expr, aitr *AuxiliaryFields) *OutputEdge {
//	switch expr := expr.(type) {
//	case *influxql.VarRef:
//		return aitr.Iterator(expr)
//	case *influxql.BinaryExpr:
//		lhs := buildAuxIterator(expr.LHS, aitr)
//		rhs := buildAuxIterator(expr.RHS, aitr)
//
//		bexpr := &BinaryExpr{
//			LHS:  lhs,
//			RHS:  rhs,
//			Op:   expr.Op,
//			Desc: fmt.Sprintf("%s %s %s", expr.LHS, expr.Op, expr.RHS),
//		}
//		lhs.Node, rhs.Node = bexpr, bexpr
//
//		var out *OutputEdge
//		bexpr.Output, out = NewEdge(bexpr)
//		return out
//	case *influxql.ParenExpr:
//		return buildAuxIterator(expr.Expr, aitr)
//	default:
//		panic("unimplemented")
//	}
//}
//
//func buildFieldIterators(stmt *influxql.SelectStatement) ([]*OutputEdge, error) {
//	edges := make([]*OutputEdge, len(stmt.Fields))
//	for i, field := range stmt.Fields {
//		out, err := buildExprIterator(field.Expr, stmt.Sources)
//		if err != nil {
//			return nil, err
//		}
//		edges[i] = out
//	}
//	return edges, nil
//}
//
//func buildExprIterator(expr influxql.Expr, sources influxql.Sources) (*OutputEdge, error) {
//	switch expr := expr.(type) {
//	case *influxql.VarRef:
//		merge := &Merge{}
//		for _, source := range sources {
//			switch source := source.(type) {
//			case *influxql.Measurement:
//				ic := &IteratorCreator{Expr: expr, Measurement: source}
//				ic.Output = merge.AddInput(ic)
//			default:
//				return nil, fmt.Errorf("unimplemented source type: %T", source)
//			}
//		}
//
//		var out *OutputEdge
//		merge.Output, out = NewEdge(merge)
//		return out, nil
//	case *influxql.Call:
//		switch expr.Name {
//		case "count":
//			fallthrough
//		case "min", "max", "sum", "first", "last", "mean":
//			arg0 := expr.Args[0].(*influxql.VarRef)
//			out, err := buildExprIterator(arg0, sources)
//			if err != nil {
//				return nil, err
//			}
//
//			call := &FunctionCall{Name: expr.Name, Input: out}
//			call.Output, out = out.Append(call)
//			return out, nil
//		default:
//			return nil, errors.New("unimplemented")
//		}
//	default:
//		return nil, fmt.Errorf("invalid expression type: %T", expr)
//	}
//}
