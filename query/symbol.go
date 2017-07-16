package query

import (
	"github.com/influxdata/influxdb/influxql"
)

type Symbol interface {
	// resolve will resolve the Symbol with a Node and attach it to the WriteEdge.
	resolve(m ShardGroup, c *compiledField, out *WriteEdge)
}

type SymbolTable struct {
	Table map[*WriteEdge]Symbol
}

type AuxiliarySymbol struct {
	Ref *influxql.VarRef
}

func (s *AuxiliarySymbol) resolve(m ShardGroup, c *compiledField, out *WriteEdge) {
	// Determine the type for the reference if it hasn't been resolved yet.
	ref := *s.Ref
	ref.Type = influxql.EvalType(&ref, c.global.Sources, m)
	c.global.AuxiliaryFields.Iterator(&ref, out)
}

type AuxiliaryWildcardSymbol struct{}

func (s *AuxiliaryWildcardSymbol) resolve(m ShardGroup, c *compiledField, out *WriteEdge) {
	symbol := AuxiliarySymbol{Ref: c.WildcardRef}
	symbol.resolve(m, c, out)
}

type VarRefSymbol struct {
	Ref *influxql.VarRef
}

func (s *VarRefSymbol) resolve(m ShardGroup, c *compiledField, out *WriteEdge) {
	// Determine the type for the reference if it hasn't been resolved yet.
	ref := *s.Ref
	ref.Type = influxql.EvalType(&ref, c.global.Sources, m)

	merge := &Merge{Output: out}
	for _, source := range c.global.Sources {
		switch source := source.(type) {
		case *influxql.Measurement:
			ic := &IteratorCreator{
				Expr:            s.Ref,
				AuxiliaryFields: c.global.AuxiliaryFields,
				Measurement:     source,
			}
			ic.Output = merge.AddInput(ic)
		default:
			panic("unimplemented")
		}
	}
	out.Node = merge
}

type WildcardSymbol struct{}

func (s *WildcardSymbol) resolve(m ShardGroup, c *compiledField, out *WriteEdge) {
	symbol := VarRefSymbol{Ref: c.WildcardRef}
	symbol.resolve(m, c, out)
}
