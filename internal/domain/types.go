package domain

type User struct {
	ID                   string `json:"id"`
	Email                string `json:"email"`
	DisplayName          string `json:"display_name"`
	Role                 string `json:"role"`
	Disabled             bool   `json:"disabled"`
	Revision             int64  `json:"revision"`
	AuthorizationVersion int64  `json:"authorization_version"`
	CredentialsVersion   int64  `json:"-"`
}
type Actor struct {
	User
	TokenHash string
}
type Me struct {
	User
	WorkspaceID string `json:"workspace_id"`
	Profile     string `json:"profile"`
}
type Version struct {
	Content      int64 `json:"content_version"`
	Conversation int64 `json:"conversation_version"`
	Cycle        int64 `json:"cycle_no"`
}

func (v Version) Equal(w Version) bool { return v == w }

type Ticket struct {
	ID            string  `json:"id"`
	DisplayID     string  `json:"display_id"`
	Title         string  `json:"title"`
	Description   string  `json:"description"`
	RequesterID   string  `json:"requester_id"`
	RequesterName string  `json:"requester_name"`
	AssigneeID    *string `json:"assignee_id"`
	AssigneeName  *string `json:"assignee_name"`
	Category      string  `json:"category"`
	Priority      string  `json:"priority"`
	Status        string  `json:"status"`
	Revision      int64   `json:"revision"`
	Version
	ActiveJobID     *string `json:"active_job_id,omitempty"`
	ApprovedReplyID *string `json:"approved_reply_id"`
	CreatedAt       string  `json:"created_at"`
	UpdatedAt       string  `json:"updated_at"`
	ResolvedAt      *string `json:"resolved_at"`
}
type Comment struct {
	ID         string `json:"id"`
	TicketID   string `json:"ticket_id"`
	Cycle      int64  `json:"cycle_no"`
	AuthorName string `json:"author_name"`
	Visibility string `json:"visibility"`
	Body       string `json:"body"`
	CreatedAt  string `json:"created_at"`
}
type Reply struct {
	ID         string `json:"id"`
	Cycle      int64  `json:"cycle_no"`
	Body       string `json:"body"`
	AuthorName string `json:"author_name"`
	CreatedAt  string `json:"created_at"`
}
type Article struct {
	ID        string   `json:"id"`
	DisplayID string   `json:"display_id"`
	Title     string   `json:"title"`
	Body      string   `json:"body"`
	Tags      []string `json:"tags"`
	Version   int64    `json:"version"`
	Archived  bool     `json:"archived"`
	UpdatedAt string   `json:"updated_at"`
}
type Source struct {
	ArticleID  string `json:"article_id"`
	EvidenceID string `json:"evidence_id"`
	Version    int64  `json:"version"`
	Title      string `json:"title"`
	Excerpt    string `json:"excerpt"`
	BodyHash   string `json:"body_hash"`
}
type SourceHead struct {
	ArticleID string `json:"article_id"`
	Version   int64  `json:"version"`
	Archived  bool   `json:"archived"`
}
type ReviewContext struct {
	Token    string       `json:"token"`
	Heads    []SourceHead `json:"heads"`
	Required []string     `json:"required_acknowledgements"`
}
type AIOutput struct {
	Summary              string   `json:"summary"`
	Category             string   `json:"suggested_category"`
	Priority             string   `json:"suggested_priority"`
	Reply                string   `json:"reply_text"`
	EvidenceIDs          []string `json:"evidence_ids"`
	NeedsMoreInformation *bool    `json:"needs_more_information"`
}
type Draft struct {
	ID       string `json:"id"`
	JobID    string `json:"job_id"`
	TicketID string `json:"ticket_id"`
	Version
	Mode string `json:"provider_mode"`
	AIOutput
	ContextOmitted bool          `json:"context_omitted"`
	Warnings       []string      `json:"warning_codes"`
	Sources        []Source      `json:"sources"`
	ReviewState    string        `json:"review_state"`
	ReviewContext  ReviewContext `json:"review_context"`
	Valid          bool          `json:"valid"`
	CreatedAt      string        `json:"created_at"`
}
type Input struct {
	TicketID    string `json:"ticket_id"`
	Title       string `json:"title"`
	Description string `json:"description"`
	Version
	Comments       []Comment `json:"public_comments"`
	ContextOmitted bool      `json:"context_omitted"`
}
type Pricing struct {
	Input    float64 `json:"input_per_million"`
	Output   float64 `json:"output_per_million"`
	Currency string  `json:"currency"`
}
type AIConfig struct {
	Mode             string   `json:"mode"`
	Strategy         string   `json:"strategy"`
	BaseURL          string   `json:"base_url"`
	Model            string   `json:"model"`
	TimeoutSeconds   int      `json:"timeout_seconds"`
	OutputTokenLimit int      `json:"output_token_limit"`
	Pricing          *Pricing `json:"pricing"`
	Version          int64    `json:"config_version"`
}
type Settings struct {
	AIConfig
	HasAPIKey         bool   `json:"has_api_key"`
	MaskedKey         string `json:"masked_key"`
	KeystoreAvailable bool   `json:"keystore_available"`
	LiveAllowed       bool   `json:"live_allowed"`
}
type Call struct {
	Ordinal           int     `json:"call_ordinal"`
	Status            string  `json:"status"`
	DispatchConfirmed bool    `json:"dispatch_confirmed"`
	DurationMS        int64   `json:"duration_ms"`
	InputTokens       *int64  `json:"input_tokens"`
	OutputTokens      *int64  `json:"output_tokens"`
	CachedTokens      *int64  `json:"cached_tokens"`
	UsageComplete     bool    `json:"usage_complete"`
	ErrorCode         *string `json:"error_code"`
}
type Step struct {
	Ordinal    int     `json:"ordinal"`
	Kind       string  `json:"kind"`
	State      string  `json:"state"`
	StartedAt  string  `json:"started_at"`
	FinishedAt *string `json:"finished_at"`
}
type RuntimeHint struct {
	StorageBlocked         bool   `json:"storage_blocked"`
	IntendedTerminalState  string `json:"intended_terminal_state"`
	PendingResultAvailable bool   `json:"pending_result_available"`
	ErrorCode              string `json:"error_code"`
}
type Job struct {
	ID            string       `json:"id"`
	TicketID      string       `json:"ticket_id"`
	ActorID       string       `json:"actor_id"`
	ParentJobID   *string      `json:"parent_job_id"`
	Mode          string       `json:"provider_mode"`
	Strategy      string       `json:"execution_strategy"`
	Configuration AIConfig     `json:"configuration"`
	Status        string       `json:"status"`
	ErrorCode     *string      `json:"error_code"`
	ErrorStage    *string      `json:"error_stage"`
	RequestID     string       `json:"request_id"`
	CreatedAt     string       `json:"created_at"`
	StartedAt     *string      `json:"started_at"`
	FinishedAt    *string      `json:"finished_at"`
	Steps         []Step       `json:"steps"`
	Calls         []Call       `json:"model_calls"`
	RuntimeHint   *RuntimeHint `json:"runtime_hint,omitempty"`
}
type JobRecord struct {
	Job
	Input                     Input
	Config                    AIConfig
	Secret                    []byte
	ActorAuthorizationVersion int64
}
type Review struct {
	ID        string  `json:"id"`
	DraftID   *string `json:"draft_id"`
	Decision  string  `json:"decision"`
	Reason    *string `json:"reason_code"`
	CreatedAt string  `json:"created_at"`
}
type Audit struct {
	ID        string         `json:"id"`
	ActorName string         `json:"actor_name"`
	Action    string         `json:"action"`
	Details   map[string]any `json:"details"`
	CreatedAt string         `json:"created_at"`
}
type Assignee struct {
	ID          string `json:"id"`
	DisplayName string `json:"display_name"`
}
type Page[T any] struct {
	Items    []T `json:"items"`
	Page     int `json:"page"`
	PageSize int `json:"page_size"`
	Total    int `json:"total"`
}
type Dashboard struct {
	Total           int                 `json:"total"`
	Open            int                 `json:"open"`
	InProgress      int                 `json:"in_progress"`
	AwaitingReview  int                 `json:"awaiting_review"`
	Resolved        int                 `json:"resolved"`
	Generated       int                 `json:"generated"`
	DemoGenerated   int                 `json:"demo_generated"`
	LiveGenerated   int                 `json:"live_generated"`
	Approved        int                 `json:"approved"`
	Rejected        int                 `json:"rejected"`
	Manual          int                 `json:"manual"`
	Assignees       []Assignee          `json:"assignees,omitempty"`
	AIMode          *string             `json:"ai_mode,omitempty"`
	AIStrategy      *string             `json:"ai_strategy,omitempty"`
	AIConfigVersion *int64              `json:"ai_config_version,omitempty"`
	StorageBlocked  *bool               `json:"storage_blocked,omitempty"`
	WorkspaceMode   *string             `json:"workspace_mode,omitempty"`
	Setup           *WorkspaceReadiness `json:"setup,omitempty"`
}

type WorkspaceReadiness struct {
	KnowledgeArticles int  `json:"knowledge_articles"`
	RequesterAccounts int  `json:"requester_accounts"`
	StaffAccounts     int  `json:"staff_accounts"`
	ModelConfigured   bool `json:"model_configured"`
}
type TicketDetail struct {
	Ticket   Ticket      `json:"ticket"`
	Comments []Comment   `json:"comments"`
	Replies  []Reply     `json:"replies"`
	Work     *TicketWork `json:"work,omitempty"`
}

// TicketWork is staff-only and is read in the same transaction as the ticket.
type TicketWork struct {
	Drafts []Draft `json:"drafts"`
	Jobs   []Job   `json:"jobs"`
}

func Staff(u User) bool { return u.Role == "admin" || u.Role == "agent" }
func Enum(v string, choices ...string) bool {
	for _, x := range choices {
		if x == v {
			return true
		}
	}
	return false
}
func Ptr[T any](v T) *T { return &v }
