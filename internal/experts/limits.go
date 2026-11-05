package experts

// Limits on what one expert file may hold. They keep a single conversation's
// system prompt bounded and the picker readable. They are defaults chosen by
// hand, not measured; a file over a limit is skipped with a message that names it.
const (
	// MaxRulesBytes caps the rules text (the Markdown after the front matter). It
	// is added to the system prompt of every model call of a conversation.
	MaxRulesBytes = 8000
	// MinRulesRunes rejects a file that has a name but nothing to follow.
	MinRulesRunes = 20

	MaxNameRunes    = 40
	MaxSummaryRunes = 120
	MaxCategoryRune = 24
	MaxSkills       = 8
	MaxSkillRunes   = 64
	MaxStarters     = 6
	MaxStarterRunes = 200

	// maxFileBytes bounds what is read from one user file before parsing.
	maxFileBytes = 64 << 10
	// maxFilesPerDir bounds how many files of one folder are looked at.
	maxFilesPerDir = 100

	// defaultOrder places an expert that sets no order after the ones that do.
	defaultOrder = 100
)
