package store

// SetSessionTitleIfUnnamed preserves manual renames and avoids overwriting
// usage or other session fields updated while the title request was running.
func (s *Store) SetSessionTitleIfUnnamed(id, title string) (bool, error) {
	result, err := s.DB.Exec(`UPDATE sessions SET title=?, updated_at=? WHERE id=? AND (title='' OR title='New chat')`, title, Now(), id)
	if err != nil {
		return false, err
	}
	n, err := result.RowsAffected()
	return n > 0, err
}
