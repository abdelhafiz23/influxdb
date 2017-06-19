package query

import (
	"errors"
	"fmt"

	"github.com/influxdata/influxdb/influxql"
)

func Compile(stmt *influxql.SelectStatement) ([]*Edge, error) {
	// Retrieve refs for each call and var ref.
	info := newSelectInfo(stmt)
	if len(info.calls) > 1 && len(info.refs) > 0 {
		return nil, errors.New("cannot select fields when selecting multiple aggregates")
	}

	// If there are multiple auxilary fields and no calls then construct an aux iterator.
	if len(info.calls) == 0 && len(info.refs) > 0 {
		return buildAuxIterators(stmt, info)
	}
	return nil, errors.New("unimplemented")
}

func buildAuxIterators(stmt *influxql.SelectStatement, info *selectInfo) ([]*Edge, error) {
	// Determine auxiliary fields to be selected.
	//	auxFields := make([]VarRef, 0, len(info.refs))
	//	for ref := range info.refs {
	//		auxFields = append(auxFields, *ref)
	//	}
	//	sort.Sort(VarRefs(auxFields))

	// Create IteratorCreator node.
	merge := &Merge{}
	for _, source := range stmt.Sources {
		switch source := source.(type) {
		case *influxql.Measurement:
			ic := &IteratorCreator{Measurement: source}
			ic.Output = merge.AddInput(ic)
		default:
			panic("unimplemented")
		}
	}

	fields := &AuxiliaryFields{}
	merge.Output, fields.Input = AddEdge(merge, fields)
	outputs := make([]*Edge, 0, len(stmt.Fields))
	for _, field := range stmt.Fields {
		outputs = append(outputs, buildAuxIterator(field.Expr, fields))
	}
	return outputs, nil
}

func buildAuxIterator(expr influxql.Expr, aitr *AuxiliaryFields) *Edge {
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
		lhs.Output, rhs.Output = bexpr, bexpr
		bexpr.Output, _ = AddEdge(bexpr, nil)
		return bexpr.Output
	case *influxql.ParenExpr:
		return buildAuxIterator(expr.Expr, aitr)
	default:
		panic("unimplemented")
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
