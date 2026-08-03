package readingroom

func SeminarProfiles() []AgentProfile {
	profiles := DefaultProfiles()
	return []AgentProfile{profiles[RoleLibrarian], profiles[RoleSummarizer], profiles[RoleCritic], profiles[RoleConnector], profiles[RoleQuestioner], profiles[RoleCitationVerifier], profiles[RoleSynthesizer]}
}
