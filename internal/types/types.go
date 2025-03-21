package types

type Article struct {
	ID            int    `db:"id" json:"id"`
	Title         string `db:"title" json:"title"`
	Author        string `db:"author" json:"author"`
	Summary       string `db:"summary" json:"summary"`
	DateRead      string `db:"date_read" json:"dateRead"`
	DatePublished string `db:"date_published" json:"datePublished"`
	Link          string `db:"link" json:"link"`
	ImagePath     string `db:"img_path" json:"img_path"`
	Type          int    `db:"type" json:"type"`
}
