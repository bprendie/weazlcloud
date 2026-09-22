package users

import "path/filepath"

func (s *Store) DataPath(u User) (string, error) {
	if len(u.ID) != 32 {
		return "", ErrBadUserID
	}
	for _, c := range u.ID {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return "", ErrBadUserID
		}
	}
	return filepath.Join(s.userRoot, u.ID), nil
}

func (s *Store) VaultPath(u User) string   { return filepath.Join(s.userRoot, u.ID, "vault.json") }
func (s *Store) NodeKeyPath(u User) string { return filepath.Join(s.userRoot, u.ID, "node.key") }
func (s *Store) LibraryPath(u User) string { return filepath.Join(s.userRoot, u.ID, "library") }
func (s *Store) CatalogPath(u User) string { return filepath.Join(s.userRoot, u.ID, "catalog.enc") }
func (s *Store) PlacesPath(u User) string  { return filepath.Join(s.userRoot, u.ID, "places.json") }
func (s *Store) LegacyVaultPath() string {
	return filepath.Join(filepath.Dir(s.userRoot), "vault.json")
}
func (s *Store) LegacyNodeKeyPath() string {
	return filepath.Join(filepath.Dir(s.userRoot), "node.key")
}
func (s *Store) LegacyLibraryPath() string { return filepath.Join(filepath.Dir(s.userRoot), "library") }
func (s *Store) LegacyCatalogPath() string {
	return filepath.Join(filepath.Dir(s.userRoot), "catalog.enc")
}
func (s *Store) LegacyPlacesPath() string {
	return filepath.Join(filepath.Dir(s.userRoot), "places.json")
}
