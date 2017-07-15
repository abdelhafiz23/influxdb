package query

import (
	"container/list"
	"fmt"
	"reflect"
)

func Clone(out *ReadEdge) *ReadEdge {
	cloner := newCloner()
	return cloner.Clone(out)
}

// cloner contains the state used when cloning a graph.
type cloner struct {
	// Nodes contains a mapping of old nodes to their clones.
	Nodes map[Node]Node

	// ReadEdges contains a mapping of old read edges to their clones.
	ReadEdges map[*ReadEdge]*ReadEdge

	// WriteEdges contains a mapping of old write edges to their clones.
	WriteEdges map[*WriteEdge]*WriteEdge
}

func newCloner() *cloner {
	return &cloner{
		Nodes:      make(map[Node]Node),
		ReadEdges:  make(map[*ReadEdge]*ReadEdge),
		WriteEdges: make(map[*WriteEdge]*WriteEdge),
	}
}

func (c *cloner) Clone(out *ReadEdge) *ReadEdge {
	// Iterate through the ReadEdge and clone every node.
	c.cloneNodes(out.Input.Node)
	fmt.Println(len(c.Nodes))
	fmt.Println(len(c.ReadEdges))
	fmt.Println(len(c.WriteEdges))
	return nil
}

func (c *cloner) cloneNodes(n Node) {
	if n == nil {
		return
	}

	unvisited := list.New()
	unvisited.PushBack(n)

	for unvisited.Len() > 0 {
		e := unvisited.Front()
		unvisited.Remove(e)

		node := e.Value.(Node)
		if _, ok := c.Nodes[node]; ok {
			continue
		}
		v := reflect.ValueOf(node)
		c.Nodes[node] = c.clone(v, unvisited).Interface().(Node)
	}
}

func (c *cloner) clone(v reflect.Value, unvisited *list.List) reflect.Value {
	switch v.Kind() {
	case reflect.Ptr:
		// Check if the thing it points to is a read edge or a write edge.
		if v.IsNil() {
			return v
		}
		clone := c.clone(v.Elem(), unvisited)
		return clone.Addr()
	case reflect.Struct:
		// If the struct is a read or write edge, instantiate a zero version
		// and then record their memory locations.
		if typ := v.Type(); typ.AssignableTo(reflect.TypeOf(ReadEdge{})) {
			// Retrieve the node from this read edge.
			other := v.Addr().Interface().(*ReadEdge)
			if other.Input != nil && other.Input.Node != nil {
				unvisited.PushBack(other.Input.Node)
			}
			edge := &ReadEdge{}
			c.ReadEdges[v.Addr().Interface().(*ReadEdge)] = edge
			return reflect.ValueOf(edge).Elem()
		} else if typ.AssignableTo(reflect.TypeOf(WriteEdge{})) {
			edge := &WriteEdge{}
			c.WriteEdges[v.Addr().Interface().(*WriteEdge)] = edge
			return reflect.ValueOf(edge).Elem()
		}

		// Iterate through all of the fields in the original node and clone them.
		clone := reflect.New(v.Type()).Elem()
		for i := 0; i < v.NumField(); i++ {
			f := v.Field(i)
			clone.Field(i).Set(c.clone(f, unvisited))
		}
		return clone
	case reflect.Slice:
		// Construct a new slice for the specified type with the same length.
		clone := reflect.MakeSlice(v.Type(), v.Len(), v.Len())
		for i := 0; i < v.Len(); i++ {
			clone.Index(i).Set(c.clone(v.Index(i), unvisited))
		}
		return clone
	default:
		clone := reflect.New(v.Type()).Elem()
		clone.Set(v)
		return clone
	}
}
