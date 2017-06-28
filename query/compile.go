package query

import (
	"errors"
	"fmt"

	"github.com/influxdata/influxdb/influxql"
)

type compiledStatement struct {
	// Sources holds the data sources this will query from.
	Sources influxql.Sources

	// FunctionCalls holds a reference to the output edge of all of the
	// function calls that have been instantiated.
	FunctionCalls []*OutputEdge

	// OnlySelectors is set to true when there are no aggregate functions.
	OnlySelectors bool

	// TopBottomFunction is set to top or bottom when one of those functions are
	// used in the statement.
	TopBottomFunction string

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
		OnlySelectors: true,
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

	merge := &Merge{}
	for _, source := range c.Sources {
		switch source := source.(type) {
		case *influxql.Measurement:
			ic := &IteratorCreator{
				Expr:            arg0,
				AuxiliaryFields: &c.AuxiliaryFields,
				Measurement:     source,
			}
			ic.Output = merge.AddInput(ic)
		default:
			panic("unimplemented")
		}
	}
	call := &FunctionCall{Name: expr.Name}
	merge.Output, call.Input = AddEdge(merge, call)

	// Mark down some meta properties related to the function for query validation.
	switch expr.Name {
	case "top", "bottom":
		if c.TopBottomFunction == "" {
			c.TopBottomFunction = expr.Name
		}
	case "max", "min", "first", "last", "percentile", "sample":
	default:
		c.OnlySelectors = false
	}

	var out *OutputEdge
	call.Output, out = NewEdge(call)
	c.FunctionCalls = append(c.FunctionCalls, out)
	return out, nil
}

func (c *compiledStatement) linkAuxiliaryFields() error {
	if c.AuxiliaryFields == nil {
		if len(c.FunctionCalls) == 0 {
			return errors.New("at least 1 non-time field must be queried")
		}
		return nil
	}

	if c.AuxiliaryFields != nil {
		if !c.OnlySelectors {
			return fmt.Errorf("mixing aggregate and non-aggregate queries is not supported")
		} else if len(c.FunctionCalls) > 1 {
			return fmt.Errorf("mixing multiple selector functions with tags or fields is not supported")
		}

		if len(c.FunctionCalls) == 1 {
			c.AuxiliaryFields.Input, c.AuxiliaryFields.Output = c.FunctionCalls[0].Insert(c.AuxiliaryFields)
		} else {
			// Create a default IteratorCreator for this AuxiliaryFields.
			merge := &Merge{}
			for _, source := range c.Sources {
				switch source := source.(type) {
				case *influxql.Measurement:
					ic := &IteratorCreator{
						AuxiliaryFields: &c.AuxiliaryFields,
						Measurement:     source,
					}
					ic.Output = merge.AddInput(ic)
				default:
					panic("unimplemented")
				}
			}
			merge.Output, c.AuxiliaryFields.Input = AddEdge(merge, c.AuxiliaryFields)
		}
	}
	return nil
}

func (c *compiledStatement) validateFields() error {
	// Ensure there are not multiple calls if top/bottom is present.
	if len(c.FunctionCalls) > 1 && c.TopBottomFunction != "" {
		return fmt.Errorf("selector function %s() cannot be combined with other functions", c.TopBottomFunction)
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

	if err := c.linkAuxiliaryFields(); err != nil {
		return nil, err
	}
	if err := c.validateFields(); err != nil {
		return nil, err
	}
	return c, nil
}

func (c *compiledStatement) Select(plan *Plan) ([]*OutputEdge, error) {
	for _, out := range c.OutputEdges {
		plan.AddTarget(out)
	}
	return c.OutputEdges, nil
}
