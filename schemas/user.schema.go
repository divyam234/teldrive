package schemas

type AccountStats struct {
	TotalSize  int64  `json:"totalSize"`
	TotalFiles int64  `json:"totalFiles"`
	ChId       int64  `json:"channelId,omitempty"`
	ChName     string `json:"channelName,omitempty"`
}

type Channel struct {
	ChannelID   int64  `json:"channelId"`
	ChannelName string `json:"channelName"`
}
