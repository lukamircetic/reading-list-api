package database

import (
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
