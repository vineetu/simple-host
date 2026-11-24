package rules

type Limits struct {
	ExprBytes, ExprNodes, ExprDepth, ListItems, Policies, NameBytes, PredicateBytes, PredicateArgs int
}

func DefaultLimits() Limits {
	return Limits{ExprBytes: 2048, ExprNodes: 256, ExprDepth: 32, ListItems: 64,
		Policies: 32, NameBytes: 64, PredicateBytes: 8192, PredicateArgs: 256}
}
