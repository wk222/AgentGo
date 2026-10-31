//go:build !milvus

package memory

import (
	"context"
	"errors"
)

// MilvusStore is a lightweight no-op stub when built without `-tags milvus`,
// avoiding ~9 MB of Milvus/etcd/gogo-protobuf/grpc dependencies in the default desktop binary.
type MilvusStore struct{}

func connectMilvusStore(_ context.Context, _ BootConfig) (*MilvusStore, error) {
	return nil, errors.New("milvus support not compiled in (rebuild with -tags milvus to enable)")
}

func (s *MilvusStore) Ingest(_ context.Context, _ Record) error {
	return nil
}

func (s *MilvusStore) Recall(_ context.Context, _ string, _ RecallOptions) ([]Record, error) {
	return nil, nil
}

func (s *MilvusStore) Feedback(_ context.Context, _ string, _ FeedbackKind) error {
	return nil
}

func (s *MilvusStore) ContextPrompt(_ context.Context, _ string) (string, error) {
	return "", nil
}
