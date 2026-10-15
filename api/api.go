package api

const (
	MaxRequestBytes = 1 << 20
	MaxBodyBytes    = 64 << 10
	MaxMessageBytes = 16 << 10
	MaxKeyBytes     = 512
	MaxTitleBytes   = 4 << 10
	MaxContextBytes = 16 << 10
	MaxTags         = 64
	MaxTagBytes     = 256
	MaxKeywordBytes = 256
	MaxPropsBytes   = 64 << 10
)

const KeyPrefix = "bk_"

// DiffStatPrefix starts the progress a hook sends at session end with the diff of the whole
// checkout, so it never stands in for a turn summary. It names files, not words anyone wrote,
// so it stays readable when the rest of the session's text is private.
const DiffStatPrefix = "git diff --stat\n"

// GlobalProjectKey names the caller's personal scope, which only its owner reads.
const GlobalProjectKey = "global"

const MemorySource = "claudeMemory"

// TagRemoved marks the version memory sync writes when a memory file is deleted
// or its scope changes.
const TagRemoved = "removed"

const (
	CodeValidation        = "validation"
	CodeBadRequest        = "badRequest"
	CodeNotFound          = "notFound"
	CodeNoRepository      = "noRepository"
	CodeSessionEnded      = "sessionEnded"
	CodePaymentRequired   = "paymentRequired"
	CodeLeaseHeld         = "leaseHeld"
	CodeConflict          = "conflict"
	CodeInvalidTransition = "invalidTransition"
	CodeUnauthorized      = "unauthorized"
	CodeForbidden         = "forbidden"
	CodeTooManyRequests   = "tooManyRequests"
	CodeInternal          = "internal"
)

type ErrorBody struct {
	Error ErrorDetail `json:"error"`
}

type ErrorDetail struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

type Health struct {
	Status           string `json:"status"`
	Version          string `json:"version,omitempty"`
	MinClientVersion string `json:"minClientVersion,omitempty"`
}
