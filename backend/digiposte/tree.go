package digiposte

import (
	"context"
	"fmt"
	"io"
	"mime"
	"path"
	"strings"
	"time"

	digiposte "github.com/holyhope/digiposte-go-sdk/v1"
	"github.com/rclone/rclone/fs"
	"github.com/rclone/rclone/fs/hash"
)

func (f *Fs) buildTree(ctx context.Context) error {
	if f.tree != nil {
		return nil
	}

	folders, err := f.client.ListFolders(ctx)
	if err != nil {
		return fmt.Errorf("list folders: %w", err)
	}

	documents, err := f.client.ListDocuments(ctx)
	if err != nil {
		return fmt.Errorf("list documents: %w", err)
	}

	profile, err := f.client.GetProfile(ctx, digiposte.ProfileModeDefault)
	if err != nil {
		return fmt.Errorf("get profile: %w", err)
	}

	f.tree = &Tree{
		Folder: &digiposte.Folder{
			InternalID:    "",
			Name:          "",
			CreatedAt:     profile.Offer.SubscriptionDate,
			UpdatedAt:     profile.Offer.SubscriptionDate,
			Folders:       folders.Folders,
			DocumentCount: int64(len(documents.Documents)),
		},
		fs: f,
	}

	return nil
}

// Tree represents the root of the digiposte tree
type Tree struct {
	*digiposte.Folder

	fs fs.Info
}

// GetFolder returns the folder at the given path
func (f *Fs) GetFolder(ctx context.Context, remote string) (*digiposte.Folder, error) {
	remote = strings.Trim(remote, "/")

	if remote == "" {
		return f.tree.Folder, nil
	}

	paths := strings.Split(remote, "/")
	folder := f.tree.Folder

	for _, p := range paths {
		p := local2Remote(remote2Local(p))

		found := false
		for _, f := range folder.Folders {
			if f.Name == p {
				folder = f
				found = true
				break
			}
		}

		if !found {
			return nil, fs.ErrorDirNotFound
		}
	}

	return folder, nil
}

var _ fs.DirEntry = (*Tree)(nil)

// String returns a description of the Object
func (t *Tree) String() string {
	return ""
}

// Remote returns the remote path
func (t *Tree) Remote() string {
	return ""
}

// Fs returns read only access to the Fs that this object is part of
func (t *Tree) Fs() fs.Info {
	return t.fs
}

// ModTime returns the modification date of the file
// It should return a best guess if one isn't available
func (t *Tree) ModTime(ctx context.Context) time.Time {
	return t.CreatedAt
}

// Size returns the size of the file
func (t *Tree) Size() int64 {
	return t.DocumentCount + int64(len(t.Folders))
}

// DocumentsTotalCount returns the total count of documents in the tree
func (t *Tree) DocumentsTotalCount() int64 {
	return documentsTotalCount(t.Folder)
}

func documentsTotalCount(folder *digiposte.Folder) int64 {
	total := folder.DocumentCount

	for _, f := range folder.Folders {
		total += documentsTotalCount(f)
	}

	return total
}

// Folder represents a digiposte directory
type Folder struct {
	*digiposte.Folder
	remote string

	fs fs.Info

	client *digiposte.Client
}

// Fs returns read only access to the Fs that this object is part of
func (f *Folder) Fs() fs.Info {
	return f.fs
}

var _ fs.Directory = (*Folder)(nil)

// String returns a description of the Object
func (f *Folder) String() string {
	return f.remote
}

// Remote returns the remote path
func (f *Folder) Remote() string {
	return f.remote
}

// ModTime returns the modification date of the file
// It should return a best guess if one isn't available
func (f *Folder) ModTime(context.Context) time.Time {
	return f.UpdatedAt
}

// Size returns the size of the file
func (f *Folder) Size() int64 {
	locations := []digiposte.Location{digiposte.LocationInbox, digiposte.LocationSafe}
	if strings.HasPrefix(f.remote, digiposte.TrashDirName+"/") {
		locations = []digiposte.Location{digiposte.LocationTrashInbox, digiposte.LocationTrashSafe}
	}

	result, err := f.client.SearchDocuments(context.Background(), f.InternalID, digiposte.OnlyDocumentLocatedAt(locations...))
	if err != nil {
		fs.Errorf(f, "search in %q: %v", f.InternalID, err)

		return 0
	}

	total := int64(0)
	for _, document := range result.Documents {
		total += document.Size
	}

	for _, folder := range f.Folders {
		total += (&Folder{
			Folder: folder,
			remote: path.Join(f.remote, remote2Local(folder.Name)),
			client: f.client,
			fs:     f.fs,
		}).Size()
	}

	return total
}

// Items returns the count of items in this directory or this
// directory and subdirectories if known, -1 for unknown
func (f *Folder) Items() int64 {
	return documentsTotalCount(f.Folder)
}

// ID returns the ID of the Object if known, or "" if not
func (f *Folder) ID() string {
	return string(f.InternalID)
}

// GetTier returns storage tier or class of the Object
func (f *Folder) GetTier() string {
	panic(fmt.Errorf("not implemented"))
}

// Document represents a digiposte file
type Document struct {
	*digiposte.Document
	remote string

	fs *Fs

	client *digiposte.Client
}

var _ interface {
	fs.Object
	fs.MimeTyper
	fs.IDer
	fs.GetTierer
	fs.DirEntry
} = (*Document)(nil)

// ID returns the ID of the Object if known, or "" if not
func (d *Document) ID() string {
	return string(d.Document.InternalID)
}

// MimeType returns the content type of the Object if
// known, or "" if not
func (d *Document) MimeType(ctx context.Context) string {
	return d.Document.MimeType
}

// String returns a description of the Object
func (d *Document) String() string {
	return d.remote
}

// Remote returns the remote path
func (d *Document) Remote() string {
	return d.remote
}

// Size returns the size of the file
func (d *Document) Size() int64 {
	return d.Document.Size
}

// ModTime returns the modification date of the file
// It should return a best guess if one isn't available
func (d *Document) ModTime(context.Context) time.Time {
	return d.CreatedAt
}

// SetModTime sets the metadata on the object to set the modification date
func (d *Document) SetModTime(ctx context.Context, t time.Time) error {
	return fs.ErrorCantSetModTimeWithoutDelete
}

// Fs returns read only access to the Fs that this object is part of
func (d *Document) Fs() fs.Info {
	return d.fs
}

// Hash returns the selected checksum of the file
// If no checksum is available it returns ""
func (d *Document) Hash(ctx context.Context, ty hash.Type) (string, error) {
	return "", nil
}

// Storable says whether this object can be stored
func (d *Document) Storable() bool {
	return true
}

// GetTier returns storage tier or class of the Object
func (d *Document) GetTier() string {
	return d.Document.Location
}

// Metadata returns metadata for an object
//
// It should return nil if there is no Metadata
func (d *Document) Metadata(ctx context.Context) (fs.Metadata, error) {
	result := make(fs.Metadata, len(d.Document.UserTags))

	for _, tag := range d.Document.UserTags {
		key, value, _ := strings.Cut(tag, "=")
		result[key] = value

	}

	return result, nil
}

// Update in to the object with the modTime given of the given size
//
// When called from outside an Fs by rclone, src.Size() will always be >= 0.
// But for unknown-sized objects (indicated by src.Size() == -1), Upload should either
// return an error or update the object properly (rather than e.g. calling panic).
func (d *Document) Update(ctx context.Context, in io.Reader, src fs.ObjectInfo, options ...fs.OpenOption) error {
	return fmt.Errorf("not implemented")
}

// Open opens the file for read.  Call Close() on the returned io.ReadCloser
func (d *Document) Open(ctx context.Context, options ...fs.OpenOption) (io.ReadCloser, error) {
	for _, option := range options {
		if option.Mandatory() {
			return nil, fmt.Errorf("mandatory option %q not supported", option.String())
		}
	}

	content, contentType, err := d.client.DocumentContent(ctx, d.InternalID)
	if err != nil {
		return nil, fmt.Errorf("document content: %w", err)
	}

	mediatype, _, err := mime.ParseMediaType(contentType)
	if err != nil {
		fs.Debugf(d, "Failed to check content type %q: %v", contentType, err)
	} else if mediatype != d.Document.MimeType {
		fs.Logf(d, "Content type mismatch: %q != %q", mediatype, d.Document.MimeType)
	}

	return content, nil
}

// Remove removes this object
func (d *Document) Remove(ctx context.Context) error {
	if err := d.client.Trash(ctx, []digiposte.DocumentID{d.InternalID}, nil); err != nil {
		return fmt.Errorf("trash: %w", err)
	}

	parent, err := d.fs.GetFolder(ctx, path.Dir(d.remote))
	if err != nil {
		fs.Logf(d, "Failed to update cache: %v", err)
		return nil
	}

	parent.DocumentCount--

	return nil
}

func (f *Fs) newDocument(parentDir string, obj *digiposte.Document) *Document {
	return &Document{
		Document: obj,
		remote:   path.Join(parentDir, remote2Local(obj.Name)),
		fs:       f,
		client:   f.client,
	}
}

func (f *Fs) newFolder(parentDir string, obj *digiposte.Folder) *Folder {
	return &Folder{
		Folder: obj,
		remote: path.Join(parentDir, remote2Local(obj.Name)),
		fs:     f,
		client: f.client,
	}
}
