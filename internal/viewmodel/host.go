// SPDX-License-Identifier: BSD-3-Clause

package viewmodel

import "github.com/go-theca/gui/internal/vault"

// DefaultHost is this user's profile file, the sessions of the theca command
// and the theca binary that starts them.
func DefaultHost() (Host, error) {
	st, err := vault.ProfileStore()
	if err != nil {
		return Host{}, err
	}
	return Host{Store: st, Sessions: vault.Sessions{}, Spawner: vault.Spawner{}}, nil
}
