package launcher

import (
	"context"
	"io"
)

type API interface {
	Login(context.Context, string, string) (string, error)
	Games(context.Context, string) (Manifest, error)
	Status(context.Context, string) (ServerStatus, error)
	Addons(context.Context, string) ([]Addon, error)
	DownloadURL(context.Context, string, string) (string, error)
	AddonDownloads(context.Context, string, []int64) ([]AddonDownload, error)
	Download(context.Context, string, io.Writer) error
}

type Manifest struct {
	Success bool         `json:"success"`
	Data    ManifestData `json:"data"`
}

type ManifestData struct {
	Common struct {
		Files []ManifestFile `json:"files"`
	} `json:"common"`
	Games []Game `json:"games"`
}

type Game struct {
	Slug      string         `json:"slug"`
	Realmlist string         `json:"realmlist"`
	Files     []ManifestFile `json:"files"`
}

type ManifestFile struct {
	ID           string  `json:"id"`
	Path         string  `json:"file_path_from_game_root"`
	Hash         string  `json:"file_hash"`
	OptionSlug   *string `json:"option_slug"`
	FileSizeByte int64   `json:"file_size_bytes"`
}

type ServerStatus struct {
	Online     bool   `json:"online"`
	ServerName string `json:"serverName"`
}

type Addon struct {
	ID            int64  `json:"id"`
	Name          string `json:"name"`
	Description   string `json:"description"`
	FileSizeBytes any    `json:"file_size_bytes"`
	UpdatedAt     string `json:"updated_at"`
}

type AddonDownload struct {
	FileID   int64  `json:"file_id"`
	Filename string `json:"filename"`
	URL      string `json:"url"`
}

type LoginPrompt interface {
	PromptLogin(context.Context, string, bool) (username, password string, remember bool, err error)
}

type Progress struct {
	Stage   string
	Message string
	Path    string
	Current int
	Total   int
	Bytes   int64
}

type ProgressFunc func(Progress)
type Credentials struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

type CredentialStore struct {
	Root string
}

type Authenticator struct {
	API      API
	Store    *CredentialStore
	Prompt   LoginPrompt
	ReadOnly bool
	Warning  string
}

type UpdateOptions struct {
	GameSlug string
	Quick    bool
	Full     bool
	Verify   bool
	DryRun   bool
	Quiet    bool
}

type UpdateResult struct {
	Game             Game
	Files            []ManifestFile
	Token            string
	Checked          int
	Current          int
	Downloaded       int
	Missing          int
	Mismatched       int
	FailedPaths      []string
	FilesUpdated     bool
	RealmlistUpdated bool
	PlannedPaths     []string
	BytesBefore      int64
	BytesAfter       int64
}
