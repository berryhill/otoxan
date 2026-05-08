package auth

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"time"

	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

// infisicalBaseURL is overridable for tests.
var infisicalBaseURL = "https://app.infisical.com"

// InfisicalGet fetches a raw secret by name from the Infisical REST API.
// It reads INFISICAL_TOKEN, INFISICAL_PROJECT_ID, and INFISICAL_ENV from the environment.
func InfisicalGet(name string) (string, error) {
	token := os.Getenv("INFISICAL_TOKEN")
	projectID := os.Getenv("INFISICAL_PROJECT_ID")
	env := os.Getenv("INFISICAL_ENV")

	if token == "" {
		return "", fmt.Errorf("INFISICAL_TOKEN not set")
	}
	if projectID == "" {
		return "", fmt.Errorf("INFISICAL_PROJECT_ID not set")
	}
	if env == "" {
		env = "dev"
	}

	url := fmt.Sprintf(
		"%s/api/v3/secrets/raw/%s?workspaceId=%s&environment=%s",
		infisicalBaseURL, name, projectID, env,
	)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", fmt.Errorf("infisical request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("infisical fetch: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("infisical returned %d", resp.StatusCode)
	}

	var payload struct {
		Secret struct {
			SecretValue string `json:"secretValue"`
		} `json:"secret"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return "", fmt.Errorf("infisical decode: %w", err)
	}

	return payload.Secret.SecretValue, nil
}

// MongoClient returns a connected mongo.Client and the database name.
// It first checks the MONGO_URI env var, then falls back to InfisicalGet("MONGO_URI").
func MongoClient(ctx context.Context) (*mongo.Client, string, error) {
	uri := os.Getenv("MONGO_URI")
	if uri == "" {
		var err error
		uri, err = InfisicalGet("MONGO_URI")
		if err != nil {
			return nil, "", fmt.Errorf("mongo URI not available: %w", err)
		}
	}

	dbName := os.Getenv("MONGO_DB")
	if dbName == "" {
		dbName = "otoxan"
	}

	client, err := mongo.Connect(options.Client().ApplyURI(uri))
	if err != nil {
		return nil, dbName, fmt.Errorf("mongo connect: %w", err)
	}

	if err := client.Ping(ctx, nil); err != nil {
		return nil, dbName, fmt.Errorf("mongo ping: %w", err)
	}

	return client, dbName, nil
}
