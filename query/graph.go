package query

import (
	"fmt"
	"sync"

	"github.com/influxdata/influxdb/influxql"
)

type Node struct {
	// Input is the Edge that creates this Node.
	// All Nodes must have an input edge.
	Input Edge

	// Output is the Edge that will receive the Iterator created for this Node.
	// A node does not need to have an output edge.
	Output Edge

	itr   influxql.Iterator
	ready bool
	mu    sync.RWMutex
}

// Iterator returns the Iterator created for this Node by the Input edge.
// If the Node returns false from Ready(), this function will panic.
func (n *Node) Iterator() influxql.Iterator {
	n.mu.RLock()
	if !n.ready {
		n.mu.RUnlock()
		panic("attempted to retrieve an iterator from a node before it was ready")
	}
	itr := n.itr
	n.mu.RUnlock()
	return itr
}

// SetIterator marks this Node as ready and sets the Iterator as the returned
// iterator. If the Node has already been set, this panics. This should only be
// called from the Input Edge.
func (n *Node) SetIterator(itr influxql.Iterator) {
	n.mu.Lock()
	defer n.mu.Unlock()

	if n.ready {
		panic("unable to call SetIterator on the same node twice")
	}
	n.itr = itr
	n.ready = true
}

func (n *Node) Ready() (ready bool) {
	n.mu.RLock()
	ready = n.ready
	n.mu.RUnlock()
	return ready
}

type Edge interface {
	Inputs() []*Node
	Outputs() []*Node
	Execute() error
}

func AllInputsReady(e Edge) bool {
	inputs := e.Inputs()
	if len(inputs) == 0 {
		return true
	}

	for _, input := range inputs {
		if !input.Ready() {
			return false
		}
	}
	return true
}

type IteratorCreator struct {
	Measurement *influxql.Measurement
	OutputNode  Node
}

func (ic *IteratorCreator) Inputs() []*Node  { return nil }
func (ic *IteratorCreator) Outputs() []*Node { return []*Node{&ic.OutputNode} }

func (ic *IteratorCreator) Execute() error {
	fmt.Println("create iterator", ic.Measurement)
	ic.OutputNode.SetIterator(nil)
	return nil
}

type Merge struct {
	InputNodes []*Node
	OutputNode Node
}

func (m *Merge) Inputs() []*Node  { return m.InputNodes }
func (m *Merge) Outputs() []*Node { return []*Node{&m.OutputNode} }

func (m *Merge) Execute() error {
	fmt.Printf("merge %d nodes\n", len(m.InputNodes))
	m.OutputNode.SetIterator(nil)
	return nil
}
