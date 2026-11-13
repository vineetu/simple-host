package hackentries

// judgeMemberSQL is the one current-judge definition. Aliases are code constants.
func judgeMemberSQL(memberAlias, eventAlias string) string {
	return "(" + memberAlias + ".role='judge' OR (" + memberAlias + ".role='organiser' AND " + memberAlias + ".user_id = ANY(" + eventAlias + ".organiser_judges)))"
}

// JudgeMemberSQL shares the predicate with event count projections.
func JudgeMemberSQL(memberAlias, eventAlias string) string {
	return judgeMemberSQL(memberAlias, eventAlias)
}
