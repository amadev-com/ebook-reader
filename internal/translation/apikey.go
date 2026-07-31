package translation

import (
	"errors"
	"os"
)

// errNoAPIKey is returned when the OPENAI_API_KEY environment variable is not
// set. It is shared by Client and BatchClient.
var errNoAPIKey = errors.New("OPENAI_API_KEY environment variable is not set")

// apiKeyFromEnv reads the OpenAI API key from the OPENAI_API_KEY environment
// variable. Returns an empty string if not set.
func apiKeyFromEnv() string {
	return os.Getenv("OPENAI_API_KEY")
}
