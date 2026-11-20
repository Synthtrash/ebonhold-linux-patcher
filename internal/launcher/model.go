package launcher

import (
	"context"
	"encoding/json"
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
	ID         string  `json:"id"`
	Path       string  `json:"file_path_from_game_root"`
	Hash       string  `json:"file_hash"`
	OptionSlug *string `json:"option_slug"`
}

func (f *ManifestFile) UnmarshalJSON(data []byte) error {
	type plain ManifestFile
	var decoded struct {
		*plain
		ID json.RawMessage `json:"id"`
	}
	decoded.plain = (*plain)(f)
	if err := json.Unmarshal(data, &decoded); err != nil {
		return err
	}
	if len(decoded.ID) == 0 {
		return nil
	}
	if err := json.Unmarshal(decoded.ID, &f.ID); err == nil {
		return nil
	}
	var id json.Number
	if err := json.Unmarshal(decoded.ID, &id); err != nil {
		return err
	}
	f.ID = id.String()
	return nil
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
	PromptLogin(context.Context) (username, password string, err error)
}

type Progress struct {
	Stage   string
	Message string
	Path    string
	Current int
	Issue   string // Verification result: "missing", "mismatch", or "unreadable".
	Total   int
}

type ProgressFunc func(Progress)
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
	GameSlug     string
	Quick        bool
	Full         bool
	Verify       bool
	DryRun       bool
	Language     string
	ManageHD     bool
	HDCharacters bool
	HDCreatures  bool
	OptionSlugs  []string
}

type GameOption struct {
	Slug   string
	Kind   string
	Locale string
	Paths  []string
}

type FileFailure struct {
	Path   string
	Reason string
}

type UpdateResult struct {
	Token            string
	Checked          int
	Current          int
	Downloaded       int
	Missing          int
	Mismatched       int
	FailedFiles      []FileFailure
	FilesUpdated     bool
	RealmlistUpdated bool
	PlannedPaths     []string
	RemovedPaths     []string
}
