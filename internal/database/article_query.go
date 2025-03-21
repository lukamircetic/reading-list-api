package database

import (
	"database/sql"
	"log"
	"reading-list-api/internal/types"
)

func (s *service) GetAllArticles() (*[]types.Article, error) {
	articles := make([]types.Article, 0)
	query := `
		select * from articles;
	`
	err := s.db.Select(&articles, query)
	if err != nil {
		log.Println("error querying articles", err)
		return nil, err
	}
	return &articles, nil
}

func (s *service) ArticleExists(link string) (bool, error) {
	article := types.Article{}
	query := `select * from articles where link = $1;`
	err := s.db.Get(&article, query, link)
	if err == sql.ErrNoRows {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}
