package access

type CodeSender interface {
	SendSignInCode(string, string, string) error
}
type NoticeSender interface {
	SendNotice(string, string, string) error
}
