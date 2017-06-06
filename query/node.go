package query

type Node struct {
	Name    string
	Inputs  []*Edge
	Outputs []*Edge
}

type Edge struct {
	Source *Node
	Target *Node
}

func FindRootNodes(targets []*Node) []*Node {
	var rootNodes []*Node
	visited := make(map[*Node]struct{})
	for _, n := range targets {
		var walkFn func(n *Node)
		walkFn = func(n *Node) {
			if _, ok := visited[n]; ok {
				return
			}

			visited[n] = struct{}{}
			if len(n.Inputs) == 0 {
				rootNodes = append(rootNodes, n)
				return
			}

			for _, input := range n.Inputs {
				if input.Source != nil {
					walkFn(input.Source)
				}
			}
		}
		walkFn(n)
	}
	return rootNodes
}

func TopologicalSort(want []*Node) ([]*Node, error) {
	S := make(map[*Node]struct{})
	for _, n := range FindRootNodes(want) {
		S[n] = struct{}{}
	}

	var L []*Node
	visited := make(map[*Node]struct{})
	for len(S) > 0 {
		var n *Node
		for n = range S {
			break
		}
		delete(S, n)

		visited[n] = struct{}{}
		L = append(L, n)

		// Check the node's outputs to see if any of the target nodes are now
		// ready to be considered.
		for _, out := range n.Outputs {
			ok := true
			for _, input := range out.Target.Inputs {
				// Check if the dependency has been visited.
				if _, ok2 := visited[input.Source]; !ok2 {
					ok = false
					break
				}
			}

			if ok {
				S[out.Target] = struct{}{}
			}
		}
	}
	return L, nil
}
