package identity

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestFromContext_Missing(t *testing.T) {
	p, ok := FromContext(context.Background())

	assert.False(t, ok)
	assert.False(t, p.Authenticated)
}

func TestFromContext_Present(t *testing.T) {
	want := Principal{Subject: "zitadel-001", UserID: "user-uuid", ClientID: "client-1", Authenticated: true}

	ctx := WithPrincipal(context.Background(), want)
	got, ok := FromContext(ctx)

	assert.True(t, ok)
	assert.Equal(t, want, got)
}

func TestWithPrincipal_Overwrites(t *testing.T) {
	first := Principal{Subject: "a", Authenticated: true}
	second := Principal{Subject: "b", Authenticated: true}

	ctx := WithPrincipal(WithPrincipal(context.Background(), first), second)
	got, ok := FromContext(ctx)

	assert.True(t, ok)
	assert.Equal(t, "b", got.Subject)
}
