package setting

// Content moderation is separate from payment checkout. Prompt Sift uses the
// Waffo Pancake merchant id and private key already loaded for payments.
// ContentModerationEnabled is "auto", "true", or "false". "auto" (and an
// empty value) turns moderation on when those merchant credentials exist.
var (
	ContentModerationEnabled   = "auto"
	ContentModerationBlocklist string
)
