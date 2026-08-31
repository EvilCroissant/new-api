package model

import (
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestSumUsedQuotaPreservesQuotaWhenScanningRpmTpm(t *testing.T) {
	previousLogDB := LOG_DB
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&Log{}))
	LOG_DB = db
	t.Cleanup(func() {
		LOG_DB = previousLogDB
	})

	now := time.Now().Unix()
	require.NoError(t, db.Create(&Log{
		CreatedAt:        now,
		Type:             LogTypeConsume,
		Username:         "stat-user",
		ModelName:        "test-model",
		Quota:            500,
		PromptTokens:     100,
		CompletionTokens: 50,
	}).Error)
	require.NoError(t, db.Create(&Log{
		CreatedAt:        now,
		Type:             LogTypeConsume,
		Username:         "stat-user",
		ModelName:        "test-model",
		Quota:            700,
		PromptTokens:     200,
		CompletionTokens: 150,
	}).Error)

	stat, err := SumUsedQuota(
		LogTypeConsume,
		now-60,
		now+60,
		"test-model",
		"stat-user",
		"",
		0,
		"",
	)
	require.NoError(t, err)
	require.Equal(t, 1200, stat.Quota)
	require.Equal(t, 2, stat.Rpm)
	require.Equal(t, 500, stat.Tpm)
}
