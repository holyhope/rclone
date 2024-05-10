package digiposte

import (
	"context"
	"errors"
	"fmt"
	"io"
	"path"
	"strings"
	"sync"
	"time"

	digiposte "github.com/holyhope/digiposte-go-sdk/v1"
	digiconfig "github.com/rclone/rclone/backend/digiposte/config"
	"github.com/rclone/rclone/fs"
	"github.com/rclone/rclone/fs/config/configmap"
	"github.com/rclone/rclone/fs/hash"
)

// Fs represents a remote Digiposte filesystem.
type Fs struct {
	name    string
	root    string
	baseURL string
	client  *digiposte.Client

	rootFolders []*digiposte.Folder

	tree *Tree
	lock *sync.RWMutex
}

var _ fs.Fs = (*Fs)(nil)

// SlashReplacement is the character used to replace slashes in remote paths
const SlashReplacement = "_"

func remote2Local(remote string) string {
	return strings.ReplaceAll(remote, "/", SlashReplacement)
}

func local2Remote(local string) string {
	return strings.ReplaceAll(local, SlashReplacement, "/")
}

// NewFs constructs a Digiposte FS.
func NewFs(ctx context.Context, name, root string, m configmap.Mapper) (fs.Fs, error) {
	digiposteClient, err := getClient(ctx, name, m)
	if err != nil {
		return nil, err
	}

	return &Fs{
		name:        name,
		root:        root,
		baseURL:     digiconfig.APIURL(m),
		client:      digiposteClient,
		rootFolders: nil,
		tree:        nil,
		lock:        &sync.RWMutex{},
	}, nil
}

// Name returns the configured name of the file system
func (f *Fs) Name() string {
	return f.name
}

// Root returns the root for the filesystem
func (f *Fs) Root() string {
	return f.root
}

// String returns the URL for the filesystem
func (f *Fs) String() string {
	return f.baseURL
}

// Features returns the optional features of this Fs
func (f *Fs) Features() *fs.Features {
	return &fs.Features{
		BucketBased:             false,
		BucketBasedRootOK:       false,
		CanHaveEmptyDirectories: true,
		CaseInsensitive:         false,
		DuplicateFiles:          true,
		FilterAware:             false,
		GetTier:                 true,
		IsLocal:                 false,
		NoMultiThreading:        true,
		Overlay:                 false,
		PartialUploads:          false,
		ReadMetadata:            true,
		ReadMimeType:            true,
		ServerSideAcrossConfigs: false,
		SetTier:                 false,
		SlowModTime:             false,
		SlowHash:                false,
		UserMetadata:            false,
		WriteMetadata:           false,
		WriteMimeType:           true,

		About:         f.About,
		ChangeNotify:  nil,
		CleanUp:       f.CleanUp,
		Copy:          f.Copy,
		DirCacheFlush: f.DirCacheFlush,
		DirMove:       f.DirMove,
		Disconnect:    f.Disconnect,
		Move:          f.Move,
		PublicLink:    f.PublicLink,
		Purge:         f.Purge,
		ListR:         nil,
		PutStream:     f.PutStream,
		Shutdown:      f.Shutdown,
		UserInfo:      f.UserInfo,
		MergeDirs:     f.MergeDirs,
	}
}

// DirCacheFlush resets the directory cache - used in testing
// as an optional interface
func (f *Fs) DirCacheFlush() {
	f.tree = nil
}

// Shutdown the backend, closing any background tasks and any
// cached connections.
func (f *Fs) Shutdown(ctx context.Context) error {
	return nil
}

// UserInfo returns info about the connected user
func (f *Fs) UserInfo(ctx context.Context) (map[string]string, error) {
	profile, err := f.client.GetProfile(ctx, digiposte.ProfileModeDefault)
	if err != nil {
		return nil, fmt.Errorf("get profile: %w", err)
	}

	return map[string]string{
		"firstName": profile.UserInfo.FirstName,
		"lastName":  profile.UserInfo.LastName,
		"email":     profile.UserInfo.Email,
		"login":     profile.UserInfo.Login,
	}, nil
}

// Disconnect the current user
func (f *Fs) Disconnect(ctx context.Context) error {
	if err := f.client.Logout(ctx); err != nil {
		return fmt.Errorf("logout: %w", err)
	}

	return nil
}

// About gets quota information from the Fs
func (f *Fs) About(ctx context.Context) (*fs.Usage, error) {
	f.lock.RLock()
	defer f.lock.RUnlock()

	if err := f.buildTree(ctx); err != nil {
		return nil, fmt.Errorf("build tree: %w", err)
	}

	profile, err := f.client.GetProfile(ctx, digiposte.ProfileModeDefault)
	if err != nil {
		return nil, fmt.Errorf("get profile: %w", err)
	}

	trashed, err := f.trashedUsage(ctx)
	if err != nil {
		return nil, fmt.Errorf("trashed: %w", err)
	}

	return &fs.Usage{
		Total:   fs.NewUsageValue(profile.SpaceMax),
		Used:    fs.NewUsageValue(profile.SpaceUsed),
		Free:    fs.NewUsageValue(profile.SpaceFree),
		Other:   fs.NewUsageValue(profile.SpaceNotComputed),
		Objects: fs.NewUsageValue(f.tree.DocumentsTotalCount()),
		Trashed: fs.NewUsageValue(trashed),
	}, nil
}

// CleanUp the trash in the Fs
//
// Implement this if you have a way of emptying the trash or
// otherwise cleaning up old versions of files.
func (f *Fs) CleanUp(ctx context.Context) error {
	documentsResult, err := f.client.GetTrashedDocuments(ctx)
	if err != nil {
		return fmt.Errorf("get trashed documents: %w", err)
	}

	documentIDs := make([]digiposte.DocumentID, len(documentsResult.Documents))
	for i, document := range documentsResult.Documents {
		documentIDs[i] = document.InternalID
	}

	foldersResult, err := f.client.GetTrashedFolders(ctx)
	if err != nil {
		return fmt.Errorf("get trashed folders: %w", err)
	}

	folderIDs := make([]digiposte.FolderID, len(foldersResult.Folders))

	for i, folder := range foldersResult.Folders {
		folderIDs[i] = folder.InternalID
	}

	if err := f.client.Delete(ctx, documentIDs, folderIDs); err != nil {
		return fmt.Errorf("delete: %w", err)
	}

	return nil
}

func (f *Fs) trashedUsage(ctx context.Context) (int64, error) {
	documentsResult, err := f.client.GetTrashedDocuments(ctx)
	if err != nil {
		return 0, fmt.Errorf("get trashed documents: %w", err)
	}

	total := int64(0)
	for _, document := range documentsResult.Documents {
		total += document.Size
	}

	foldersResult, err := f.client.GetTrashedFolders(ctx)
	if err != nil {
		return total, fmt.Errorf("get trashed folders: %w", err)
	}

	for _, folder := range foldersResult.Folders {
		total += f.newFolder(digiposte.TrashDirName, folder).Size()
	}

	return total, nil
}

// PublicLink generates a public link to the remote path (usually readable by anyone)
func (f *Fs) PublicLink(ctx context.Context, remote string, expire fs.Duration, unlink bool) (link string, err error) {
	f.lock.RLock()
	defer f.lock.RUnlock()

	if unlink {
		return f.deletePublicLink(ctx, remote)
	}

	return f.createPublicLink(ctx, remote, expire)
}

// PublicLink generates a public link to the remote path (usually readable by anyone)
func (f *Fs) createPublicLink(ctx context.Context, remote string, expire fs.Duration) (link string, err error) {
	if err := f.buildTree(ctx); err != nil {
		return "", fmt.Errorf("build tree: %w", err)
	}

	parent := path.Dir(remote)
	baseName := path.Base(remote)
	folder, err := f.GetFolder(ctx, parent)
	if err != nil {
		return "", fmt.Errorf("get %q: %v: %w", parent, err, fs.ErrorObjectNotFound)
	}

	for _, f := range folder.Folders {
		if f.Name == baseName {
			fs.Errorf(remote, "Can't create public link for folder")
		}
	}

	result, err := f.client.SearchDocuments(ctx, folder.InternalID)
	if err != nil {
		return "", fmt.Errorf("search in %q (%s): %w", folder.Name, folder.InternalID, err)
	}

	var documentIDs []digiposte.DocumentID

	for _, d := range result.Documents {
		if d.Name == baseName {
			documentIDs = append(documentIDs, d.InternalID)
		}
	}

	startDate := time.Now()
	var endDate time.Time

	if expire.IsSet() {
		endDate = startDate.Add(time.Duration(expire))
	}

	share, err := f.client.CreateShare(ctx, startDate, endDate, baseName, "")
	if err != nil {
		return "", fmt.Errorf("create share: %w", err)
	}

	if err := f.client.SetShareDocuments(ctx, share.InternalID, documentIDs); err != nil {
		return "", fmt.Errorf("add to share: %w", err)
	}

	return share.ShortURL, nil
}

// PublicLink generates a public link to the remote path (usually readable by anyone)
func (f *Fs) deletePublicLink(ctx context.Context, remote string) (string, error) {
	if err := f.buildTree(ctx); err != nil {
		return "", fmt.Errorf("build tree: %w", err)
	}

	baseName := path.Base(remote)

	shares, err := f.client.ListSharesWithDocuments(ctx)
	if err != nil {
		return "", fmt.Errorf("list shares: %w", err)
	}

	var founds []string

	for _, share := range shares.ShareDataAndDocuments {
		if share.ShareData.Title != baseName {
			continue
		}

		match := true
		for _, document := range share.Documents {
			if document.Name != baseName {
				match = false
				break
			}
		}

		if match {
			founds = append(founds, share.ShareData.ShortURL)
			if err := f.client.DeleteShare(ctx, share.ShareData.InternalID); err != nil {
				return "", fmt.Errorf("delete share %s: %w", share.ShareData.InternalID, err)
			}
		}
	}

	switch len(founds) {
	case 0:
		return "", fmt.Errorf("no share found for %q", remote)
	case 1:
		return founds[0], nil
	default:
		fs.Infof(remote, "Found %d shares, deleted all", len(founds))

		return founds[0], nil
	}
}

// Copy src to this remote using server-side copy operations.
//
// # This is stored with the remote path given
//
// # It returns the destination Object and a possible error
//
// Will only be called if src.Fs().Name() == f.Name()
//
// If it isn't possible then return fs.ErrorCantCopy
func (f *Fs) Copy(ctx context.Context, src fs.Object, remote string) (fs.Object, error) {
	f.lock.RLock()
	defer f.lock.RUnlock()

	if err := f.buildTree(ctx); err != nil {
		return nil, fmt.Errorf("build tree: %w", err)
	}

	srcRemote := src.Remote()
	srcBaseName := path.Base(srcRemote)
	srcParent := path.Dir(srcRemote)
	remoteBaseName := path.Base(remote)
	remoteParent := path.Dir(remote)

	remoteParentFolder, err := f.GetFolder(ctx, remoteParent)
	if err != nil {
		return nil, fmt.Errorf("get %q: %v: %w", remoteParent, err, fs.ErrorCantMove)
	}

	// No need to check if the destination exists, 2 files with the same name can't exist in the same folder

	if _, ok := src.(fs.Directory); ok {
		return nil, fmt.Errorf("unsupported object %+v: %w", src, fs.ErrorCantMove)
	}

	var srcID digiposte.DocumentID
	if ider, ok := src.(fs.IDer); ok {
		srcID = digiposte.DocumentID(ider.ID())
	} else {
		return nil, fmt.Errorf("unsupported object %+v: %w", src, fs.ErrorCantMove)
	}

	result, err := f.client.CopyDocuments(ctx, []digiposte.DocumentID{srcID})
	if err != nil {
		return nil, fmt.Errorf("copy documents %v: %w", srcID, err)
	}

	if len(result.Documents) != 1 {
		return nil, fmt.Errorf("copy documents %v: expected 1 document, got %d: %w", srcID, len(result.Documents), fs.ErrorCantCopy)
	}

	documentID := result.Documents[0].InternalID

	// Move the document if needed
	if path.Dir(srcRemote) != remoteParent {
		if err := f.client.Move(ctx, remoteParentFolder.InternalID, []digiposte.DocumentID{documentID}, nil); err != nil {
			srcParentFolder, err := f.GetFolder(ctx, srcParent)
			if err != nil {
				fs.Logf(srcParent, "Failed to update cache: %v", err)
			} else {
				srcParentFolder.DocumentCount++
			}

			return nil, fmt.Errorf("move documents %v: %w", documentID, err)
		}
	}

	remoteParentFolder.DocumentCount++

	// Rename it if needed
	if srcBaseName != remoteBaseName {
		document, err := f.client.RenameDocument(ctx, documentID, remoteBaseName)
		if err != nil {
			return nil, fmt.Errorf("rename document: %w", err)
		}

		return f.newDocument(remoteParent, document), nil
	}

	return nil, nil
}

// DirMove moves src, srcRemote to this remote at dstRemote
// using server-side move operations.
//
// Will only be called if src.Fs().Name() == f.Name()
//
// If it isn't possible then return fs.ErrorCantDirMove
//
// If destination exists then return fs.ErrorDirExists
func (f *Fs) DirMove(ctx context.Context, src fs.Fs, srcRemote, dstRemote string) error {
	f.lock.Lock()
	defer f.lock.Unlock()

	if err := f.buildTree(ctx); err != nil {
		return fmt.Errorf("build tree: %w", err)
	}

	srcBaseName := path.Base(srcRemote)
	srcParent := path.Dir(srcRemote)
	dstBaseName := path.Base(dstRemote)
	dstParent := path.Dir(dstRemote)

	dstParentFolder, err := f.GetFolder(ctx, dstParent)
	if err != nil {
		return fmt.Errorf("get %q: %v: %w", dstParent, err, fs.ErrorCantMove)
	}

	// No need to check if the destination exists, 2 files with the same name can't exist in the same folder

	var folderID digiposte.FolderID
	if dir, ok := src.(fs.Directory); ok {
		folderID = digiposte.FolderID(dir.ID())
	} else {
		return fmt.Errorf("unsupported object %+v: %w", src, fs.ErrorCantMove)
	}

	// Move the document if needed
	if path.Dir(srcRemote) != dstParent {
		if err := f.client.Move(ctx, dstParentFolder.InternalID, nil, []digiposte.FolderID{folderID}); err != nil {
			return fmt.Errorf("move document: %w", err)
		}

		srcParentFolder, err := f.GetFolder(ctx, srcParent)
		if err != nil {
			fs.Logf(srcParent, "Failed to update cache: %v", err)
		} else {
			folders := make([]*digiposte.Folder, 0, len(srcParentFolder.Folders)-1)
			for _, folder := range srcParentFolder.Folders {
				if folder.InternalID != folderID {
					folders = append(folders, folder)
				}
			}

			srcParentFolder.Folders = folders
		}

		dstParentFolder, err := f.GetFolder(ctx, dstParent)
		if err != nil {
			fs.Logf(dstParent, "Failed to update cache: %v", err)
		} else {
			folders := make([]*digiposte.Folder, 0, len(dstParentFolder.Folders)-1)
			for _, folder := range dstParentFolder.Folders {
				if folder.InternalID != folderID {
					folders = append(folders, folder)
				}
			}

			dstParentFolder.Folders = folders
		}
	}

	// Rename it if needed
	if srcBaseName != dstBaseName {
		if _, err := f.client.RenameFolder(ctx, folderID, dstBaseName); err != nil {
			return fmt.Errorf("rename document: %w", err)
		}
	}

	return nil
}

// Move src to this remote using server-side move operations.
//
// # This is stored with the remote path given
//
// # It returns the destination Object and a possible error
//
// Will only be called if src.Fs().Name() == f.Name()
//
// If it isn't possible then return fs.ErrorCantMove
func (f *Fs) Move(ctx context.Context, src fs.Object, dst string) (fs.Object, error) {
	f.lock.Lock()
	defer f.lock.Unlock()

	if err := f.buildTree(ctx); err != nil {
		return nil, fmt.Errorf("build tree: %w", err)
	}

	srcRemote := src.Remote()
	srcBaseName := path.Base(srcRemote)
	srcParent := path.Dir(srcRemote)
	dstBaseName := path.Base(dst)
	dstParent := path.Dir(dst)

	dstParentFolder, err := f.GetFolder(ctx, dstParent)
	if err != nil {
		return nil, fmt.Errorf("get %q: %v: %w", dstParent, err, fs.ErrorCantMove)
	}

	// No need to check if the destination exists, 2 files with the same name can't exist in the same folder

	if _, ok := src.(fs.Directory); ok {
		return nil, fmt.Errorf("unsupported object %+v: %w", src, fs.ErrorCantMove)
	}

	var documentID digiposte.DocumentID

	if ider, ok := src.(fs.IDer); ok {
		documentID = digiposte.DocumentID(ider.ID())
	} else {
		return nil, fmt.Errorf("unsupported object %+v: %w", src, fs.ErrorCantMove)
	}

	// Move the document if needed
	if path.Dir(srcRemote) != dstParent {
		if err := f.client.Move(ctx, dstParentFolder.InternalID, []digiposte.DocumentID{documentID}, nil); err != nil {
			return nil, fmt.Errorf("move document: %w", err)
		}

		srcParentFolder, err := f.GetFolder(ctx, srcParent)
		if err != nil {
			fs.Logf(srcParent, "Failed to update cache: %v", err)
		} else {
			srcParentFolder.DocumentCount--
		}

		dstParentFolder, err := f.GetFolder(ctx, dstParent)
		if err != nil {
			fs.Logf(dstParent, "Failed to update cache: %v", err)
		} else {
			dstParentFolder.DocumentCount++
		}
	}

	// Rename it if needed
	if srcBaseName != dstBaseName {
		document, err := f.client.RenameDocument(ctx, documentID, dstBaseName)
		if err != nil {
			return nil, fmt.Errorf("rename document: %w", err)
		}

		return f.newDocument(dstParent, document), nil
	}

	return nil, nil
}

// Returns the supported hash types of the filesystem
func (f *Fs) Hashes() hash.Set {
	return hash.Set(hash.None)
}

// Precision is the remote http file system's modtime precision, which we have no way of knowing.
// The API supports RFC3339 timestamps with infinite precision, but we don't know what the server supports.
func (f *Fs) Precision() time.Duration {
	return time.Second
}

// List the objects and directories in dir into entries.  The
// entries can be returned in any order but should be for a
// complete directory.
//
// dir should be "" to list the root, and should not have
// trailing slashes.
//
// This should return ErrDirNotFound if the directory isn't
// found.
func (f *Fs) List(ctx context.Context, dir string) (entries fs.DirEntries, err error) {
	f.lock.RLock()
	defer f.lock.RUnlock()

	if err := f.buildTree(ctx); err != nil {
		return nil, fmt.Errorf("build tree: %w", err)
	}

	folder, err := f.GetFolder(ctx, dir)
	if err != nil {
		return nil, fmt.Errorf("get %q: %w", dir, err)
	}

	for _, folder := range folder.Folders {
		entries = append(entries, f.newFolder(dir, folder))
	}

	if dir == "" {
		result, err := f.client.ListDocuments(ctx)
		if err != nil {
			return nil, fmt.Errorf("list documents: %w", err)
		}

		for _, document := range result.Documents {
			entries = append(entries, f.newDocument(dir, document))
		}
	} else {
		result, err := f.client.SearchDocuments(ctx, folder.InternalID)
		if err != nil {
			return nil, fmt.Errorf("search in %q (%s): %w", folder.Name, folder.InternalID, err)
		}

		for _, document := range result.Documents {
			entries = append(entries, f.newDocument(dir, document))
		}
	}

	return entries, nil
}

// NewObject finds the Object at remote.  If it can't be found
// it returns the error ErrorObjectNotFound.
//
// If remote points to a directory then it should return
// ErrorIsDir if possible without doing any extra work,
// otherwise ErrorObjectNotFound.
func (f *Fs) NewObject(ctx context.Context, remote string) (fs.Object, error) {
	f.lock.RLock()
	defer f.lock.RUnlock()

	if err := f.buildTree(ctx); err != nil {
		return nil, fmt.Errorf("build tree: %w", err)
	}

	parentPath := path.Dir(remote)

	folder, err := f.GetFolder(ctx, parentPath)
	if err != nil {
		if errors.Is(err, fs.ErrorDirNotFound) {
			return nil, fmt.Errorf("get %q: %v: %w", parentPath, err, fs.ErrorObjectNotFound)
		}

		return nil, fmt.Errorf("get %q: %w", parentPath, err)
	}

	name := path.Base(remote)

	result, err := f.client.SearchDocuments(ctx, folder.InternalID)
	if err != nil {
		return nil, fmt.Errorf("search in %q (%s): %w", folder.Name, folder.InternalID, err)
	}

	for _, document := range result.Documents {
		if document.Name == name {
			return f.newDocument(parentPath, document), nil
		}
	}

	for _, folder := range folder.Folders {
		if folder.Name == name {
			return nil, fs.ErrorIsDir
		}
	}

	return nil, fs.ErrorObjectNotFound
}

// Put in to the remote path with the modTime given of the given size
//
// When called from outside an Fs by rclone, src.Size() will always be >= 0.
// But for unknown-sized objects (indicated by src.Size() == -1), Put should either
// return an error or upload it properly (rather than e.g. calling panic).
//
// May create the object even if it returns an error - if so
// will return the object and the error, otherwise will return
// nil and the error
func (f *Fs) Put(ctx context.Context, in io.Reader, src fs.ObjectInfo, options ...fs.OpenOption) (fs.Object, error) {
	return f.PutStream(ctx, in, src, options...)
}

// PutStream uploads to the remote path with the modTime given of indeterminate size
//
// May create the object even if it returns an error - if so
// will return the object and the error, otherwise will return
// nil and the error
func (f *Fs) PutStream(ctx context.Context, in io.Reader, src fs.ObjectInfo, options ...fs.OpenOption) (fs.Object, error) {
	for i, option := range options {
		if option.Mandatory() {
			return nil, fmt.Errorf("unsupported mandatory option %d: %v", i, option)
		}
	}

	f.lock.Lock()
	defer f.lock.Unlock()

	if err := f.buildTree(ctx); err != nil {
		return nil, fmt.Errorf("build tree: %w", err)
	}

	parentPath := path.Dir(src.Remote())
	baseName := path.Base(src.Remote())

	parent, err := f.GetFolder(ctx, parentPath)
	if err != nil {
		return nil, fmt.Errorf("get %q: %w", parentPath, err)
	}

	for _, folder := range parent.Folders {
		if folder.Name == baseName {
			fs.Infof(src, "Found folder with the same name, ignoring it")
		}
	}

	result, err := f.client.SearchDocuments(ctx, parent.InternalID)
	if err != nil {
		return nil, fmt.Errorf("search in %q (%s): %w", parent.Name, parent.InternalID, err)
	}

	var documentIDs []digiposte.DocumentID

	for _, document := range result.Documents {
		if document.Name == baseName {
			documentIDs = append(documentIDs, document.InternalID)
		}
	}

	if len(documentIDs) > 1 {
		return nil, fmt.Errorf("found %d documents with the same name", len(documentIDs))
	}

	var obj fs.Object

	document, err := f.client.CreateDocument(ctx, parent.InternalID, baseName, in, digiposte.DocumentTypeBasic)
	if document != nil {
		parent.DocumentCount++
		obj = f.newDocument(parentPath, document)
	}
	if err != nil {
		return nil, fmt.Errorf("create document: %w", err)
	}

	if err := f.client.Delete(ctx, documentIDs, nil); err != nil {
		return obj, fmt.Errorf("delete %v: %w", documentIDs, err)
	}

	parent.DocumentCount -= int64(len(documentIDs))

	return obj, errors.ErrUnsupported
}

// Mkdir makes the directory (container, bucket)
//
// Shouldn't return an error if it already exists
func (f *Fs) Mkdir(ctx context.Context, dir string) error {
	f.lock.Lock()
	defer f.lock.Unlock()

	if err := f.buildTree(ctx); err != nil {
		return fmt.Errorf("build tree: %w", err)
	}

	parentPath := path.Dir(dir)
	baseName := path.Base(dir)

	parent, err := f.GetFolder(ctx, parentPath)
	if err != nil {
		return fmt.Errorf("get %q: %w", dir, err)
	}

	folder, err := f.client.CreateFolder(ctx, parent.InternalID, baseName)
	if err != nil {
		return fmt.Errorf("create folder: %w", err)
	}

	parent.Folders = append(parent.Folders, folder)

	return nil
}

// Rmdir removes the directory (container, bucket) if empty
//
// Return an error if it doesn't exist or isn't empty
func (f *Fs) Rmdir(ctx context.Context, dir string) error {
	f.lock.Lock()
	defer f.lock.Unlock()

	if err := f.buildTree(ctx); err != nil {
		return fmt.Errorf("build tree: %w", err)
	}

	parentPath := path.Dir(dir)
	baseName := path.Base(dir)

	parent, err := f.GetFolder(ctx, parentPath)
	if err != nil {
		return fmt.Errorf("get %q: %w", dir, err)
	}

	found := false

	folders := make([]*digiposte.Folder, 0, len(parent.Folders))
	for _, folder := range parent.Folders {
		if folder.Name != baseName {
			folders = append(folders, folder)

			continue
		}

		if found {
			fs.Infof(dir, "Found multiple folders with the same name, deleting all")
		}

		if documentsTotalCount(folder) > 0 {
			return fmt.Errorf("%q is not empty", dir)
		}

		if err := f.client.Delete(ctx, nil, []digiposte.FolderID{folder.InternalID}); err != nil {
			return fmt.Errorf("delete: %w", err)
		}

		found = true
	}

	parent.Folders = folders

	if !found {
		return fmt.Errorf("%q not found", dir)
	}

	return nil
}

// Purge all files in the directory specified
//
// Implement this if you have a way of deleting all the files
// quicker than just running Remove() on the result of List()
//
// Return an error if it doesn't exist
func (f *Fs) Purge(ctx context.Context, dir string) error {
	f.lock.Lock()
	defer f.lock.Unlock()

	if err := f.buildTree(ctx); err != nil {
		return fmt.Errorf("build tree: %w", err)
	}

	parentPath := path.Dir(dir)
	baseName := path.Base(dir)

	parent, err := f.GetFolder(ctx, parentPath)
	if err != nil {
		return fmt.Errorf("get %q: %w", dir, err)
	}

	found := false

	folders := make([]*digiposte.Folder, 0, len(parent.Folders))
	for _, folder := range parent.Folders {
		if folder.Name != baseName {
			folders = append(folders, folder)

			continue
		}

		if found {
			fs.Infof(dir, "Found multiple folders with the same name, deleting all")
		}

		if err := f.client.Delete(ctx, nil, []digiposte.FolderID{folder.InternalID}); err != nil {
			return fmt.Errorf("delete: %w", err)
		}

		found = true
	}

	parent.Folders = folders

	if !found {
		return fmt.Errorf("%q not found", dir)
	}

	return nil
}

// MergeDirs merges the contents of all the directories passed
// in into the first one and rmdirs the other directories.
func (f *Fs) MergeDirs(ctx context.Context, dirs []fs.Directory) error {
	f.lock.Lock()
	defer f.lock.Unlock()

	if err := f.buildTree(ctx); err != nil {
		return fmt.Errorf("build tree: %w", err)
	}

	if len(dirs) < 2 {
		return nil
	}

	dest := dirs[0]
	dirs = dirs[1:]

	var documentIDs []digiposte.DocumentID
	var folderIDs []digiposte.FolderID

	var folderIDsToDelete []digiposte.FolderID

	for _, dir := range dirs {
		folder, err := f.GetFolder(ctx, dir.Remote())
		if err != nil {
			return fmt.Errorf("get %q: %w", dir.Remote(), err)
		}

		folderIDsToDelete = append(folderIDsToDelete, folder.InternalID)

		for _, folder := range folder.Folders {
			folderIDs = append(folderIDs, folder.InternalID)
		}

		result, err := f.client.SearchDocuments(ctx, folder.InternalID)
		if err != nil {
			return fmt.Errorf("search in %q (%s): %w", folder.Name, folder.InternalID, err)
		}

		for _, document := range result.Documents {
			documentIDs = append(documentIDs, document.InternalID)
		}
	}

	if err := f.client.Move(ctx, digiposte.FolderID(dest.ID()), documentIDs, folderIDs); err != nil {
		return fmt.Errorf("move: %w", err)
	}

	if err := f.client.Delete(ctx, nil, folderIDsToDelete); err != nil {
		return fmt.Errorf("delete: %w", err)
	}

	return nil
}
