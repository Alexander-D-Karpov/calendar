package domain

type Notice struct {
	OwnerID ID     `json:"owner"`
	Title   string `json:"title"`
	Body    string `json:"body,omitempty"`
	URL     string `json:"url,omitempty"`
	Tag     string `json:"tag,omitempty"`
}
