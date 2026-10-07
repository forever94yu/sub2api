//go:build unit

package service

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

type stubSettingRepo struct {
	mu     sync.Mutex
	values map[string]string
}

func newStubSettingRepo() *stubSettingRepo {
	return &stubSettingRepo{values: map[string]string{}}
}

func (r *stubSettingRepo) Get(context.Context, string) (*Setting, error) { return nil, nil }
func (r *stubSettingRepo) GetValue(_ context.Context, key string) (string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.values[key], nil
}

func (r *stubSettingRepo) Set(_ context.Context, key, value string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.values[key] = value
	return nil
}
func (r *stubSettingRepo) GetMultiple(context.Context, []string) (map[string]string, error) {
	return map[string]string{}, nil
}
func (r *stubSettingRepo) SetMultiple(context.Context, map[string]string) error { return nil }
func (r *stubSettingRepo) GetAll(context.Context) (map[string]string, error) {
	return map[string]string{}, nil
}
func (r *stubSettingRepo) Delete(context.Context, string) error { return nil }

// reversibleEncryptor stands in for AES: prefixed so a test can tell ciphertext
// from plaintext, and so decrypting a plaintext value fails like the real one.
type reversibleEncryptor struct{}

func (reversibleEncryptor) Encrypt(plaintext string) (string, error) {
	return "enc:" + plaintext, nil
}

func (reversibleEncryptor) Decrypt(ciphertext string) (string, error) {
	rest, ok := strings.CutPrefix(ciphertext, "enc:")
	if !ok {
		return "", errors.New("not encrypted")
	}
	return rest, nil
}

type recordingStorage struct{ saved []string }

func (s *recordingStorage) Save(_ context.Context, key, _ string, _ []byte) (string, error) {
	s.saved = append(s.saved, key)
	return "https://cdn.example.com/" + key, nil
}

func newImageStorageFixture(t *testing.T, fallback config.ImageStorageConfig) (*ImageStorageSettingService, *stubSettingRepo, *[]config.ImageStorageConfig) {
	return newImageStorageFixtureWithKey(t, fallback, true)
}

func newImageStorageFixtureWithKey(t *testing.T, fallback config.ImageStorageConfig, encryptionKeyConfigured bool) (*ImageStorageSettingService, *stubSettingRepo, *[]config.ImageStorageConfig) {
	t.Helper()
	repo := newStubSettingRepo()
	encryptor := reversibleEncryptor{}
	backup := NewBackupService(repo, &config.Config{
		Totp: config.TotpConfig{EncryptionKeyConfigured: encryptionKeyConfigured},
	}, encryptor, nil, nil)

	var built []config.ImageStorageConfig
	factory := func(_ context.Context, cfg *config.ImageStorageConfig) (ImageStorage, error) {
		built = append(built, *cfg)
		return &recordingStorage{}, nil
	}
	return NewImageStorageSettingService(repo, encryptor, backup, factory, fallback), repo, &built
}

func seedBackupS3(t *testing.T, repo *stubSettingRepo, cfg BackupS3Config) {
	t.Helper()
	cfg.SecretAccessKey = "enc:" + cfg.SecretAccessKey
	data, err := json.Marshal(cfg)
	require.NoError(t, err)
	require.NoError(t, repo.Set(context.Background(), settingKeyBackupS3Config, string(data)))
}

// The admin switch must take effect without a restart: that is the entire point
// of moving image_storage out of config.yaml (#4542).
func TestImageStorageSettingsToggleTakesEffectWithoutRestart(t *testing.T) {
	svc, repo, built := newImageStorageFixture(t, config.ImageStorageConfig{})
	ctx := context.Background()
	seedBackupS3(t, repo, BackupS3Config{
		Endpoint: "https://acct.r2.cloudflarestorage.com", Region: "auto",
		Bucket: "backup-bucket", AccessKeyID: "ak", SecretAccessKey: "sk",
		Prefix: "backups/",
	})

	uploader, enabled := svc.resolve()
	require.False(t, enabled, "disabled until an admin turns it on")
	require.Nil(t, uploader)

	_, err := svc.Update(ctx, ImageStorageSettings{Enabled: true, ReuseBackupS3: true})
	require.NoError(t, err)

	uploader, enabled = svc.resolve()
	require.True(t, enabled, "saving the setting must enable the feature immediately")
	require.NotNil(t, uploader)

	_, err = svc.Update(ctx, ImageStorageSettings{Enabled: false, ReuseBackupS3: true})
	require.NoError(t, err)
	_, enabled = svc.resolve()
	require.False(t, enabled, "turning it back off must also apply immediately")

	require.Len(t, *built, 1, "the S3 client is built only when the feature is on")
}

func TestImageStorageSettingsReuseBackupCredentials(t *testing.T) {
	svc, repo, built := newImageStorageFixture(t, config.ImageStorageConfig{})
	ctx := context.Background()
	seedBackupS3(t, repo, BackupS3Config{
		Endpoint: "https://acct.r2.cloudflarestorage.com", Region: "wnam",
		Bucket: "backup-bucket", AccessKeyID: "backup-ak", SecretAccessKey: "backup-sk",
		Prefix: "backups/", ForcePathStyle: true,
	})

	_, err := svc.Update(ctx, ImageStorageSettings{Enabled: true, ReuseBackupS3: true, Prefix: "images"})
	require.NoError(t, err)
	_, enabled := svc.resolve()
	require.True(t, enabled)

	require.Len(t, *built, 1)
	got := (*built)[0]
	require.Equal(t, "https://acct.r2.cloudflarestorage.com", got.Endpoint)
	require.Equal(t, "wnam", got.Region)
	require.Equal(t, "backup-ak", got.AccessKeyID)
	require.Equal(t, "backup-sk", got.SecretAccessKey, "the backup secret must be decrypted before use")
	require.True(t, got.ForcePathStyle)
	require.Equal(t, "backup-bucket", got.Bucket, "an empty bucket falls back to the backup bucket")
	require.Equal(t, "images/", got.Prefix, "images stay under their own prefix so they never collide with backups/")

	// Reusing must not duplicate the secret into a second row.
	raw, err := repo.GetValue(ctx, settingKeyImageStorageConfig)
	require.NoError(t, err)
	require.NotContains(t, raw, "backup-sk")
	require.NotContains(t, raw, "enc:")
}

func TestImageStorageSettingsOwnCredentialsAreEncryptedAndMasked(t *testing.T) {
	svc, repo, built := newImageStorageFixture(t, config.ImageStorageConfig{})
	ctx := context.Background()

	saved, err := svc.Update(ctx, ImageStorageSettings{
		Enabled: true, Bucket: "my-images",
		Endpoint:    "https://acct.r2.cloudflarestorage.com",
		AccessKeyID: "ak", SecretAccessKey: "super-secret",
	})
	require.NoError(t, err)
	require.Empty(t, saved.SecretAccessKey, "the response must never echo the secret back")

	raw, err := repo.GetValue(ctx, settingKeyImageStorageConfig)
	require.NoError(t, err)
	require.NotContains(t, raw, `"secret_access_key":"super-secret"`, "the secret must be encrypted at rest")
	require.Contains(t, raw, "enc:super-secret")

	fetched, err := svc.Get(ctx)
	require.NoError(t, err)
	require.Empty(t, fetched.SecretAccessKey)
	require.True(t, svc.SecretConfigured(ctx))

	_, enabled := svc.resolve()
	require.True(t, enabled)
	require.Equal(t, "super-secret", (*built)[0].SecretAccessKey, "the stored secret must be decrypted before use")

	// An update that omits the secret keeps the stored one rather than wiping it.
	_, err = svc.Update(ctx, ImageStorageSettings{
		Enabled: true, Bucket: "my-images",
		Endpoint: "https://acct.r2.cloudflarestorage.com", AccessKeyID: "ak",
	})
	require.NoError(t, err)
	svc.resolve()
	require.Equal(t, "super-secret", (*built)[1].SecretAccessKey)
}

// Persisting the service's own S3 secret must be refused when the encryption key
// is auto-generated, otherwise the ciphertext cannot be decrypted after a
// restart (#4524). Reusing the backup credentials stays allowed because it does
// not persist a second copy of the secret.
func TestImageStorageSettingsRejectSecretWithEphemeralKey(t *testing.T) {
	svc, repo, built := newImageStorageFixtureWithKey(t, config.ImageStorageConfig{}, false)
	ctx := context.Background()

	_, err := svc.Update(ctx, ImageStorageSettings{
		Enabled: true, Bucket: "my-images",
		Endpoint:    "https://acct.r2.cloudflarestorage.com",
		AccessKeyID: "ak", SecretAccessKey: "super-secret",
	})
	require.ErrorIs(t, err, ErrSecretEncryptionKeyNotConfigured)

	raw, _ := repo.GetValue(ctx, settingKeyImageStorageConfig)
	require.Empty(t, raw, "nothing must be persisted when the secret is rejected")
	require.Empty(t, *built)

	// Reusing backup credentials does not persist a secret, so it stays allowed.
	seedBackupS3(t, repo, BackupS3Config{
		Endpoint: "https://acct.r2.cloudflarestorage.com", Region: "auto",
		Bucket: "backup-bucket", AccessKeyID: "ak", SecretAccessKey: "sk", Prefix: "backups/",
	})
	_, err = svc.Update(ctx, ImageStorageSettings{Enabled: true, ReuseBackupS3: true})
	require.NoError(t, err)
}

func TestImageStorageSettingsIncompleteStaysDisabled(t *testing.T) {
	svc, _, built := newImageStorageFixture(t, config.ImageStorageConfig{})
	ctx := context.Background()

	_, err := svc.Update(ctx, ImageStorageSettings{Enabled: true, Bucket: "my-images"})
	require.NoError(t, err)

	_, enabled := svc.resolve()
	require.False(t, enabled, "missing credentials must not enable the feature")
	require.Empty(t, *built, "no client is built from an incomplete configuration")
}

// Deployments that already enabled the feature through config.yaml must keep
// working after the setting moves into the database.
func TestImageStorageSettingsFallBackToConfigFile(t *testing.T) {
	svc, _, built := newImageStorageFixture(t, config.ImageStorageConfig{
		Enabled: true, Endpoint: "https://acct.r2.cloudflarestorage.com", Region: "auto",
		Bucket: "yaml-bucket", AccessKeyID: "yaml-ak", SecretAccessKey: "yaml-sk",
		Prefix: "images/", MaxDownloadByte: 1024,
	})

	_, enabled := svc.resolve()
	require.True(t, enabled, "config.yaml still enables the feature when nothing is stored yet")
	require.Equal(t, "yaml-bucket", (*built)[0].Bucket)

	fetched, err := svc.Get(context.Background())
	require.NoError(t, err)
	require.True(t, fetched.Enabled)
	require.Equal(t, "yaml-bucket", fetched.Bucket)
	require.Empty(t, fetched.SecretAccessKey)
}

func TestImageStorageSettingsPreserveConfigFileSecretOnFirstSave(t *testing.T) {
	svc, repo, built := newImageStorageFixture(t, config.ImageStorageConfig{
		Enabled: true, Bucket: "yaml-bucket", AccessKeyID: "yaml-ak", SecretAccessKey: "yaml-sk",
	})
	ctx := context.Background()
	fetched, err := svc.Get(ctx)
	require.NoError(t, err)
	require.True(t, svc.SecretConfigured(ctx))
	fetched.Prefix = "changed/"
	_, err = svc.Update(ctx, *fetched)
	require.NoError(t, err)

	_, enabled := svc.resolve()
	require.True(t, enabled, "saving the masked config must not disable image tasks")
	require.Len(t, *built, 1)
	require.Equal(t, "yaml-sk", (*built)[0].SecretAccessKey)
	require.Equal(t, "changed/", (*built)[0].Prefix)
	raw, err := repo.GetValue(ctx, settingKeyImageStorageConfig)
	require.NoError(t, err)
	var stored ImageStorageSettings
	require.NoError(t, json.Unmarshal([]byte(raw), &stored))
	require.Equal(t, "enc:yaml-sk", stored.SecretAccessKey)
}

func TestImageStorageSettingsConfigFileSecretRequiresDurableKey(t *testing.T) {
	svc, repo, _ := newImageStorageFixtureWithKey(t, config.ImageStorageConfig{
		Enabled: true, Bucket: "yaml-bucket", AccessKeyID: "yaml-ak", SecretAccessKey: "yaml-sk",
	}, false)
	ctx := context.Background()
	fetched, err := svc.Get(ctx)
	require.NoError(t, err)
	_, err = svc.Update(ctx, *fetched)
	require.ErrorIs(t, err, ErrSecretEncryptionKeyNotConfigured)
	raw, err := repo.GetValue(ctx, settingKeyImageStorageConfig)
	require.NoError(t, err)
	require.Empty(t, raw)
}

func TestImageStorageSettingsRefreshAfterBackupCredentialsChange(t *testing.T) {
	svc, repo, built := newImageStorageFixture(t, config.ImageStorageConfig{})
	ctx := context.Background()
	seedBackupS3(t, repo, BackupS3Config{
		Bucket: "old-bucket", AccessKeyID: "old-ak", SecretAccessKey: "old-sk",
	})
	_, err := svc.Update(ctx, ImageStorageSettings{Enabled: true, ReuseBackupS3: true})
	require.NoError(t, err)
	first, enabled := svc.resolve()
	require.True(t, enabled)
	require.Len(t, *built, 1)

	_, err = svc.backup.UpdateS3Config(ctx, BackupS3Config{
		Endpoint: "https://new.example.com", Bucket: "new-bucket", AccessKeyID: "new-ak", SecretAccessKey: "new-sk",
	})
	require.NoError(t, err)
	next, enabled := svc.resolve()
	require.True(t, enabled)
	require.NotSame(t, first, next, "credential changes must rebuild the cached uploader")
	require.Len(t, *built, 2)
	require.Equal(t, "new-bucket", (*built)[1].Bucket)
	require.Equal(t, "new-ak", (*built)[1].AccessKeyID)
	require.Equal(t, "new-sk", (*built)[1].SecretAccessKey)
	require.Equal(t, "https://new.example.com", (*built)[1].Endpoint)
	unchanged, _ := svc.resolve()
	require.Same(t, next, unchanged, "unchanged settings should keep the cached client")
}

func TestImageStorageSettingsRefreshChangesFromAnotherInstance(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		svc, repo, built := newImageStorageFixture(t, config.ImageStorageConfig{})
		ctx := context.Background()
		seedBackupS3(t, repo, BackupS3Config{
			Bucket: "old-bucket", AccessKeyID: "old-ak", SecretAccessKey: "old-sk",
		})
		_, err := svc.Update(ctx, ImageStorageSettings{Enabled: true, ReuseBackupS3: true})
		require.NoError(t, err)
		_, enabled := svc.resolve()
		require.True(t, enabled)

		// A second process shares the database but not the in-memory config version.
		otherBackup := NewBackupService(repo, &config.Config{
			Totp: config.TotpConfig{EncryptionKeyConfigured: true},
		}, reversibleEncryptor{}, nil, nil)
		_, err = otherBackup.UpdateS3Config(ctx, BackupS3Config{
			Bucket: "new-bucket", AccessKeyID: "new-ak", SecretAccessKey: "new-sk",
		})
		require.NoError(t, err)
		time.Sleep(time.Minute + time.Second)
		_, enabled = svc.resolve()
		require.True(t, enabled)
		require.Len(t, *built, 2)
		require.Equal(t, "new-sk", (*built)[1].SecretAccessKey)

		other := NewImageStorageSettingService(repo, reversibleEncryptor{}, otherBackup, svc.factory, config.ImageStorageConfig{})
		_, err = other.Update(ctx, ImageStorageSettings{Enabled: false, ReuseBackupS3: true})
		require.NoError(t, err)
		time.Sleep(time.Minute + time.Second)
		_, enabled = svc.resolve()
		require.False(t, enabled, "a remote settings change must not leave the feature enabled indefinitely")
	})
}

type connectionCheckingStorage struct {
	recordingStorage
	err     error
	checked bool
}

type imageStorageReadErrorRepo struct {
	SettingRepository
	err error
}

func (r imageStorageReadErrorRepo) GetValue(context.Context, string) (string, error) {
	return "", r.err
}

func TestS3SettingsUpdatesPreserveCredentialsOnReadFailure(t *testing.T) {
	for _, kind := range []string{"backup", "images"} {
		t.Run(kind, func(t *testing.T) {
			svc, repo, _ := newImageStorageFixture(t, config.ImageStorageConfig{})
			ctx := context.Background()
			seedBackupS3(t, repo, BackupS3Config{Bucket: "backup", AccessKeyID: "ak", SecretAccessKey: "sk"})
			_, err := svc.Update(ctx, ImageStorageSettings{Bucket: "images", AccessKeyID: "ak", SecretAccessKey: "sk"})
			require.NoError(t, err)
			readErr := errors.New("database unavailable")
			failingRepo := imageStorageReadErrorRepo{SettingRepository: repo, err: readErr}
			if kind == "backup" {
				svc.backup.settingRepo = failingRepo
				_, err = svc.backup.UpdateS3Config(ctx, BackupS3Config{Bucket: "changed", AccessKeyID: "ak"})
			} else {
				svc.settingRepo = failingRepo
				_, err = svc.Update(ctx, ImageStorageSettings{Bucket: "changed", AccessKeyID: "ak"})
			}
			require.ErrorIs(t, err, readErr)
			for _, key := range []string{settingKeyBackupS3Config, settingKeyImageStorageConfig} {
				raw, err := repo.GetValue(ctx, key)
				require.NoError(t, err)
				require.Contains(t, raw, "enc:sk")
				require.NotContains(t, raw, "changed")
			}
		})
	}
}

func (s *connectionCheckingStorage) TestConnection(context.Context) error {
	s.checked = true
	return s.err
}

func TestImageStorageSettingsTestConnectionChecksStorage(t *testing.T) {
	for _, checkErr := range []error{nil, errors.New("access denied")} {
		name := "success"
		if checkErr != nil {
			name = "rejected credentials"
		}
		t.Run(name, func(t *testing.T) {
			svc, _, _ := newImageStorageFixture(t, config.ImageStorageConfig{})
			storage := &connectionCheckingStorage{err: checkErr}
			svc.factory = func(context.Context, *config.ImageStorageConfig) (ImageStorage, error) {
				return storage, nil
			}
			err := svc.TestConnection(context.Background(), ImageStorageSettings{
				Bucket: "bucket", AccessKeyID: "ak", SecretAccessKey: "sk",
			})
			require.ErrorIs(t, err, checkErr)
			require.True(t, storage.checked, "client construction alone cannot verify connectivity")
		})
	}
}

func TestImageStorageSettingsTestConnectionUsesConfigFileSecret(t *testing.T) {
	svc, _, _ := newImageStorageFixture(t, config.ImageStorageConfig{
		Enabled: true, Bucket: "yaml-bucket", AccessKeyID: "yaml-ak", SecretAccessKey: "yaml-sk",
	})
	storage := &connectionCheckingStorage{}
	svc.factory = func(_ context.Context, cfg *config.ImageStorageConfig) (ImageStorage, error) {
		require.Equal(t, "yaml-sk", cfg.SecretAccessKey)
		return storage, nil
	}
	fetched, err := svc.Get(context.Background())
	require.NoError(t, err)
	require.NoError(t, svc.TestConnection(context.Background(), *fetched))
	require.True(t, storage.checked)
}
