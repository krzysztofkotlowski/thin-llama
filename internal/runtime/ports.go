package runtime

import "fmt"

const (
	defaultChatPort      = 11435
	defaultEmbeddingPort = 11436
)

func ResolvePorts(chatPort, embeddingPort int) (int, int, error) {
	if chatPort == 0 {
		chatPort = defaultChatPort
	}
	if embeddingPort == 0 {
		embeddingPort = defaultEmbeddingPort
	}
	if chatPort == embeddingPort {
		return 0, 0, fmt.Errorf("chat and embedding ports must differ")
	}
	return chatPort, embeddingPort, nil
}
