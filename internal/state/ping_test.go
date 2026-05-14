package state

import (
	"context"
	"testing"

	"github.com/testcontainers/testcontainers-go/modules/mongodb"
	"github.com/stretchr/testify/require"
)

func TestPing_Success(t *testing.T) {
	ctx := context.Background()
	container, err := mongodb.Run(ctx, "mongo:7")
	require.NoError(t, err)
	t.Cleanup(func() { _ = container.Terminate(ctx) })

	uri, err := container.ConnectionString(ctx)
	require.NoError(t, err)

	ResetClient()
	t.Cleanup(ResetClient)

	client, err := OpenClient(uri)
	require.NoError(t, err)
	require.NotNil(t, client)

	require.NoError(t, Ping(client))
}
