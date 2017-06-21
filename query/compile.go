package query

import (
	"errors"
	"fmt"
	"sort"

	"github.com/influxdata/influxdb/influxql"
)

func Compile(stmt *influxql.SelectStatement) ([]*OutputEdge, error) {
	// Retrieve refs for each call and var ref.
	info := newSelectInfo(stmt)
	if len(info.calls) > 1 && len(info.refs) > 0 {
		return nil, errors.New("cannot select fields when selecting multiple aggregates")
	}

	// If there are multiple auxilary fields and no calls then construct an aux iterator.
	if len(info.calls) == 0 && len(info.refs) > 0 {
		return buildAuxIterators(stmt, info)
	}
	return buildFieldIterators(stmt)
}

func buildAuxIterators(stmt *influxql.SelectStatement, info *selectInfo) ([]*OutputEdge, error) {
	// Determine auxiliary fields to be selected.
	auxFields := make([]influxql.VarRef, 0, len(info.refs))
	for ref := range info.refs {
		auxFields = append(auxFields, *ref)
	}
	sort.Sort(influxql.VarRefs(auxFields))

	// Create IteratorCreator node.
	merge := &Merge{}
	for _, source := range stmt.Sources {
		switch source := source.(type) {
		case *influxql.Measurement:
			ic := &IteratorCreator{Aux: auxFields, Measurement: source}
			ic.Output = merge.AddInput(ic)
		default:
			panic("unimplemented")
		}
	}

	var out *OutputEdge
	merge.Output, out = NewEdge(merge)

	if stmt.Limit > 0 || stmt.Offset > 0 {
		limit := &Limit{
			Limit:  stmt.Limit,
			Offset: stmt.Offset,
			Input:  out,
		}
		limit.Output, out = limit.Input.Append(limit)
	}

	fields := &AuxiliaryFields{Aux: auxFields, Input: out}
	out.Node = fields
	outputs := make([]*OutputEdge, 0, len(stmt.Fields))
	for _, field := range stmt.Fields {
		outputs = append(outputs, buildAuxIterator(field.Expr, fields))
	}
	return outputs, nil
}

func buildAuxIterator(expr influxql.Expr, aitr *AuxiliaryFields) *OutputEdge {
	switch expr := expr.(type) {
	case *influxql.VarRef:
		return aitr.Iterator(expr)
	case *influxql.BinaryExpr:
		lhs := buildAuxIterator(expr.LHS, aitr)
		rhs := buildAuxIterator(expr.RHS, aitr)

		bexpr := &BinaryExpr{
			LHS:  lhs,
			RHS:  rhs,
			Op:   expr.Op,
			Desc: fmt.Sprintf("%s %s %s", expr.LHS, expr.Op, expr.RHS),
		}
		lhs.Node, rhs.Node = bexpr, bexpr

		var out *OutputEdge
		bexpr.Output, out = NewEdge(bexpr)
		return out
	case *influxql.ParenExpr:
		return buildAuxIterator(expr.Expr, aitr)
	default:
		panic("unimplemented")
	}
}

func buildFieldIterators(stmt *influxql.SelectStatement) ([]*OutputEdge, error) {
	edges := make([]*OutputEdge, len(stmt.Fields))
	for i, field := range stmt.Fields {
		out, err := buildExprIterator(field.Expr, stmt.Sources)
		if err != nil {
			return nil, err
		}
		edges[i] = out
	}
	return edges, nil
}

func buildExprIterator(expr influxql.Expr, sources influxql.Sources) (*OutputEdge, error) {
	switch expr := expr.(type) {
	case *influxql.VarRef:
		merge := &Merge{}
		for _, source := range sources {
			switch source := source.(type) {
			case *influxql.Measurement:
				ic := &IteratorCreator{Expr: expr, Measurement: source}
				ic.Output = merge.AddInput(ic)
			default:
				return nil, fmt.Errorf("unimplemented source type: %T", source)
			}
		}

		var out *OutputEdge
		merge.Output, out = NewEdge(merge)
		return out, nil
	case *influxql.Call:
		switch expr.Name {
		case "count":
			fallthrough
		case "min", "max", "sum", "first", "last", "mean":
			arg0 := expr.Args[0].(*influxql.VarRef)
			out, err := buildExprIterator(arg0, sources)
			if err != nil {
				return nil, err
			}

			call := &FunctionCall{Name: expr.Name, Input: out}
			call.Output, out = out.Append(call)
			return out, nil
		default:
			return nil, errors.New("unimplemented")
		}
	default:
		return nil, fmt.Errorf("invalid expression type: %T", expr)
	}
}

// selectInfo represents an object that stores info about select fields.
type selectInfo struct {
	calls map[*influxql.Call]struct{}
	refs  map[*influxql.VarRef]struct{}
}

// newSelectInfo creates a object with call and var ref info from stmt.
func newSelectInfo(stmt *influxql.SelectStatement) *selectInfo {
	info := &selectInfo{
		calls: make(map[*influxql.Call]struct{}),
		refs:  make(map[*influxql.VarRef]struct{}),
	}
	influxql.Walk(info, stmt.Fields)
	return info
}

func (v *selectInfo) Visit(n influxql.Node) influxql.Visitor {
	switch n := n.(type) {
	case *influxql.Call:
		v.calls[n] = struct{}{}
		return nil
	case *influxql.VarRef:
		v.refs[n] = struct{}{}
		return nil
	}
	return v
}
