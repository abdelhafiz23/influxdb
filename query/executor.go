package query

import (
	"container/list"
)

type Plan struct {
	ready *list.List
	want  map[*Node]struct{}
}

func NewPlan() *Plan {
	return &Plan{
		ready: list.New(),
		want:  make(map[*Node]struct{}),
	}
}

func (p *Plan) AddTarget(n *Node) {
	if _, ok := p.want[n]; ok {
		return
	}

	p.want[n] = struct{}{}
	if inputs := n.Input.Inputs(); len(inputs) == 0 {
		p.ready.PushBack(n.Input)
		return
	} else {
		for _, input := range inputs {
			p.AddTarget(input)
		}
	}
}

func (p *Plan) FindWork() Edge {
	front := p.ready.Front()
	if front != nil {
		return p.ready.Remove(front).(Edge)
	}
	return nil
}

// EdgeFinished runs when notified that an Edge has finished running so the
// Edge's output Nodes can be checked to see if their output edges are now
// ready to be run.
func (p *Plan) EdgeFinished(e Edge) {
	for _, n := range e.Outputs() {
		// All of the nodes should now be considered ready.
		if !n.Ready() {
			// The edge should call SetIterator on each of its output nodes
			// after executing if there was no error.
			panic("node is not considered ready even after its input edge has been run")
		}

		// The nodes are now considered ready. Check if their output edge is
		// now ready to be executed (if they have one).
		if n.Output != nil && AllInputsReady(n.Output) {
			p.ready.PushBack(n.Output)
		}
	}
}
