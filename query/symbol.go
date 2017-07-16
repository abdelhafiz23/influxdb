package query

type Symbol interface {
	// Resolve will resolve the Symbol with a Node and attach it to the WriteEdge.
	Resolve(out *WriteEdge) error
}

type SymbolTable struct {
	Table map[*WriteEdge]Symbol
}
