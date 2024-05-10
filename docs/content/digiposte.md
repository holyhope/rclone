---
title: "Digiposte"
description: "Rclone docs for Digiposte"
versionIntroduced: "v1.65.1"
---

# {{< icon "fab fa-mailbox" >}} Digiposte

Paths are specified as `remote:path`

Digiposte is a french service provided by La Poste (the french post office) to store and share documents.

Digiposte paths may be as deep as required, e.g.
`remote:directory/subdirectory`.

## Configuration

The initial setup for digiposte involves getting credentials from Digiposte
which you need to do in your browser.  `rclone config` walks you
through it.

Here is an example of how to make a remote called `remote`.  First run:

     rclone config

This will guide you through an interactive setup process:

```
n) New remote
d) Delete remote
q) Quit config
e/n/d/q> n
name> remote
Type of storage to configure.
Choose a number from below, or type in your own value
[snip]
XX / Digiposte
   \ "digiposte"
[snip]
Storage> digiposte
Digiposte App Key - leave blank normally.
app_key>
Remote config
--------------------
[remote]
app_key =
--------------------
y) Yes this is OK
e) Edit this remote
d) Delete this remote
y/e/d> y
```

See the [remote setup docs](/remote_setup/) for how to set it up on a
machine with no Internet browser available.

Note that rclone runs a webserver on your local machine to collect the
token as returned from Dropbox. This only
runs from the moment it opens your browser to the moment you get back
the verification code.  This is on `http://127.0.0.1:53682/` and it
may require you to unblock it temporarily if you are running a host
firewall, or use manual mode.

You can then use it like this,

List directories in top level of your dropbox

    rclone lsd remote:

List all the files in your dropbox

    rclone ls remote:

To copy a local directory to a dropbox directory called backup

    rclone copy /home/source remote:backup

### Dropbox for business

Rclone supports Dropbox for business and Team Folders.

When using Dropbox for business `remote:` and `remote:path/to/file`
will refer to your personal folder.

If you wish to see Team Folders you must use a leading `/` in the
path, so `rclone lsd remote:/` will refer to the root and show you all
Team Folders and your User Folder.

You can then use team folders like this `remote:/TeamFolder` and
`remote:/TeamFolder/path/to/file`.

A leading `/` for a Dropbox personal account will do nothing, but it
will take an extra HTTP transaction so it should be avoided.

### Modified time and Hashes

Dropbox supports modified times, but the only way to set a
modification time is to re-upload the file.

This means that if you uploaded your data with an older version of
rclone which didn't support the v2 API and modified times, rclone will
decide to upload all your old data to fix the modification times.  If
you don't want this to happen use `--size-only` or `--checksum` flag
to stop it.

Dropbox supports [its own hash
type](https://www.dropbox.com/developers/reference/content-hash) which
is checked for all transfers.

### Restricted filename characters

| Character | Value | Replacement |
| --------- |:-----:|:-----------:|
| NUL       | 0x00  | ␀           |
| /         | 0x2F  | ／           |
| DEL       | 0x7F  | ␡           |
| \         | 0x5C  | ＼           |

File names can also not end with the following characters.
These only get replaced if they are the last character in the name:

| Character | Value | Replacement |
| --------- |:-----:|:-----------:|
| SP        | 0x20  | ␠           |

Invalid UTF-8 bytes will also be [replaced](/overview/#invalid-utf8),
as they can't be used in JSON strings.

### Batch mode uploads {#batch-mode}

Using batch mode uploads is very important for performance when using
the Dropbox API. See [the dropbox performance guide](https://developers.dropbox.com/dbx-performance-guide)
for more info.

There are 3 modes rclone can use for uploads.

## Get your own Digiposte App key

When you use rclone with Dropbox in its default configuration you are using rclone's App ID. This is shared between all the rclone users.

Here is how to create your own Dropbox App ID for rclone:

1. Log into the [Laposte App console](https://developer.laposte.fr/my-apps) with your Laposte Account (It need not
to be the same account as the Laposte you want to access). If you don't have an account, you can create one [here](https://developer.laposte.fr/auth/sign-up). Click on `Add an app`, choose the name of your application and click on `Add`.

2. Activate [the Digiposte API](https://developer.laposte.fr/catalog-apis/digiposte@3) on the application.

3. From the app console, click on `My apps` and then on the name of your app. Find the `Production key` values and copy them into the `rclone config` as `app_key` and `app_secret`.
