package database

import (
	"database/sql"
	"fmt"
	"log"
	"reading-list-api/internal/types"
)

func (s *service) GetAllArticles() (*[]types.Article, error) {
	articles := make([]types.Article, 0)
	query := `
		select * from articles order by date_read desc, id asc;
	`
	err := s.db.Select(&articles, query)
	if err != nil {
		log.Println("error querying articles", err)
		return nil, err
	}
	return &articles, nil
}

func (s *service) GetArticlePage(offset int, limit int) (*[]types.Article, error) {
	articles := make([]types.Article, 0)
	query := `
		select * from articles
		order by date_read desc, id asc
		limit ?
		offset ?;
	`

	err := s.db.Select(&articles, query, limit, offset)
	if err != nil {
		log.Println("error querying articles", err)
		return nil, err
	}
	return &articles, nil
}

func (s *service) GetArticleCount() (int, error) {
	var articleCount int
	query := `
		select count(*) from articles;
	`
	err := s.db.QueryRow(query).Scan(&articleCount)
	if err != nil {
		log.Println("error counting articles", err)
		return 0, err
	}
	return articleCount, nil
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

func (s *service) InsertArticle(article *types.Article) error {
	query := `
		insert into articles (
			title,
			author,
			summary,
			date_read,
			date_published,
			link,
			type
		) values(
			:title,
			:author,
			:summary,
			:date_read,
			:date_published,
			:link,
			:type
		);
	`
	_, err := s.db.NamedExec(query, &article)
	if err != nil {
		return fmt.Errorf("error inserting into db: %v", err)
	}
	return nil
}
