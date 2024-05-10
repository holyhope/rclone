// Package digiposte provides an interface to the Digiposte document storage system.
package digiposte

import (
	"slices"
	"time"

	digisettings "github.com/holyhope/digiposte-go-sdk/settings"
	digiconfig "github.com/rclone/rclone/backend/digiposte/config"

	"github.com/rclone/rclone/fs"
	"github.com/rclone/rclone/fs/config"
	"github.com/rclone/rclone/fs/config/obscure"
	"github.com/rclone/rclone/lib/batcher"
	"github.com/rclone/rclone/lib/oauthutil"
)

func init() {
	digiconfig.MustObscure = obscure.MustObscure
	digiconfig.MustReveal = obscure.MustReveal

	opts := []fs.Option{{ //nolint:exhaustruct,gochecknoglobals
		Name:    digiconfig.APIURLKey,
		Default: digisettings.DefaultAPIURL,
		Help:    `This is the base URL for Digiposte API.`,
		Examples: []fs.OptionExample{{
			Value:    digisettings.StagingAPIURL,
			Help:     `Connect to the Digiposte API staging environment.`,
			Provider: "",
		}},
		Advanced:   true,
		Sensitive:  false,
		NoPrefix:   true,
		IsPassword: false,
		Required:   false,
	}, { //nolint:exhaustruct
		Name:       digiconfig.DocumentURLKey,
		Default:    digisettings.DefaultDocumentURL,
		Help:       `This is the base URL for Digiposte document viewer.`,
		Advanced:   true,
		Sensitive:  false,
		NoPrefix:   true,
		IsPassword: false,
		Required:   false,
		Examples: []fs.OptionExample{{
			Value:    digisettings.StagingDocumentURL,
			Help:     `Connect to the Digiposte API staging environment.`,
			Provider: "",
		}},
	}, { //nolint:exhaustruct
		Name:       digiconfig.UsernameKey,
		Help:       `Username to use for authentication.`,
		Advanced:   false,
		Sensitive:  true,
		NoPrefix:   true,
		IsPassword: false,
		Required:   true,
	}, { //nolint:exhaustruct
		Name:       digiconfig.PasswordKey,
		Help:       `Password to use for authentication.`,
		Advanced:   false,
		Sensitive:  true,
		NoPrefix:   true,
		IsPassword: true,
		Required:   true,
	}, { //nolint:exhaustruct
		Name:       digiconfig.OTPSecretKey,
		Help:       `otp secret to use for authentication.`,
		Advanced:   false,
		Sensitive:  true,
		NoPrefix:   true,
		IsPassword: true,
		Required:   false,
	}}

	for _, opt := range slices.Clone(oauthutil.SharedOptions) {
		switch opt.Name {
		case config.ConfigToken: // Store the token in the config file
			opt.Advanced = true

			opts = append(opts, opt)
		default: // do nothing
		}

		opts = append(opts, opt)
	}

	fs.Register(&fs.RegInfo{
		Name:        "digiposte",
		Description: "Digiposte V3",
		NewFs:       NewFs,
		Config:      nil,
		MetadataInfo: &fs.MetadataInfo{
			Help: `Any metadata supported by the underlying remote is read and written.`,
		},
		Hide: false,
		Options: append(opts, (&batcher.Options{
			MaxBatchSize:          1000,
			DefaultTimeoutSync:    500 * time.Millisecond,
			DefaultTimeoutAsync:   10 * time.Second,
			DefaultBatchSizeAsync: 100,
		}).FsOptions("For full info see [the main docs](https://rclone.org/digiposte/#batch-mode)\n\n")...),
	})
}
