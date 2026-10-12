package service

import (
	"context"
	"database/sql"
	"encoding/json"
	"flowdesk/internal/domain"
	"flowdesk/internal/repository"
	"unicode/utf8"
)

type ArticleBody struct {
	Title           string   `json:"title"`
	Body            string   `json:"body"`
	Tags            []string `json:"tags"`
	ExpectedVersion int64    `json:"expected_version"`
	Archived        bool     `json:"archived"`
}

func validArticle(b ArticleBody) bool {
	if !Text(b.Title, 200) || !Text(b.Body, 20000) || len(b.Tags) > 10 {
		return false
	}
	for _, t := range b.Tags {
		if !Text(t, 50) {
			return false
		}
	}
	return true
}
func kbCap(ctx context.Context, q repository.Executor) error {
	var n int
	if e := q.QueryRowContext(ctx, "SELECT COUNT(*) FROM knowledge_articles WHERE archived=0").Scan(&n); e != nil {
		return e
	}
	if n >= 200 {
		return domain.E("KB_LIMIT_REACHED")
	}
	return nil
}
func (s *Service) Knowledge(ctx context.Context, a domain.Actor, page, size int, archived bool, search string) (domain.Page[domain.Article], error) {
	out := domain.Page[domain.Article]{Items: []domain.Article{}, Page: page, PageSize: size}
	if e := Staff(a); e != nil {
		return out, e
	}
	if page < 1 || page > 100000 || size < 1 || size > 100 || utf8.RuneCountInString(search) > 200 {
		return out, domain.E("VALIDATION_ERROR")
	}
	where := " WHERE (title LIKE ? OR body LIKE ?)"
	args := []any{"%" + search + "%", "%" + search + "%"}
	if !archived {
		where += " AND archived=0"
	}
	if e := s.Store.DB.QueryRowContext(ctx, "SELECT COUNT(*) FROM knowledge_articles"+where, args...).Scan(&out.Total); e != nil {
		return out, DBError(e)
	}
	rows, e := s.Store.DB.QueryContext(ctx, "SELECT id,title,body,tags_json,version,archived,updated_at FROM knowledge_articles"+where+" ORDER BY id DESC LIMIT ? OFFSET ?", append(args, size, (page-1)*size)...)
	if e != nil {
		return out, DBError(e)
	}
	defer rows.Close()
	for rows.Next() {
		var v domain.Article
		var id int64
		var tags string
		if e = rows.Scan(&id, &v.Title, &v.Body, &tags, &v.Version, &v.Archived, &v.UpdatedAt); e != nil {
			return out, DBError(e)
		}
		v.ID = repository.ID(id)
		v.DisplayID = "K" + v.ID
		if e = json.Unmarshal([]byte(tags), &v.Tags); e != nil {
			return out, DBError(e)
		}
		out.Items = append(out.Items, v)
	}
	return out, DBError(rows.Err())
}
func (s *Service) CreateArticle(ctx context.Context, a domain.Actor, key string, b ArticleBody) (Result, error) {
	return s.mutate(ctx, a, "POST /api/knowledge", key, b, staffScope, func(ctx context.Context, tx *sql.Tx, a domain.Actor) (any, int, error) {
		if !validArticle(b) || b.Archived || b.ExpectedVersion != 0 {
			return nil, 0, domain.E("VALIDATION_ERROR")
		}
		if e := kbCap(ctx, tx); e != nil {
			return nil, 0, e
		}
		now := repository.Now()
		tags := b.Tags
		if tags == nil {
			tags = []string{}
		}
		r, e := tx.ExecContext(ctx, "INSERT INTO knowledge_articles(title,body,tags_json,created_at,updated_at) VALUES(?,?,?,?,?)", b.Title, b.Body, repository.JSON(tags), now, now)
		if e != nil {
			return nil, 0, e
		}
		n, _ := r.LastInsertId()
		v, e := repository.Article(ctx, tx, repository.ID(n))
		if e != nil {
			return nil, 0, e
		}
		e = s.audit(ctx, tx, a, nil, "knowledge_created", map[string]any{"article_id": v.ID})
		return v, 201, e
	})
}
func (s *Service) PatchArticle(ctx context.Context, a domain.Actor, id, key string, b ArticleBody) (Result, error) {
	scope := func(ctx context.Context, q repository.Executor, a domain.Actor) error {
		if e := Staff(a); e != nil {
			return e
		}
		_, e := repository.Article(ctx, q, id)
		return e
	}
	return s.mutate(ctx, a, "PATCH /api/knowledge/"+id, key, b, scope, func(ctx context.Context, tx *sql.Tx, a domain.Actor) (any, int, error) {
		if !validArticle(b) {
			return nil, 0, domain.E("VALIDATION_ERROR")
		}
		v, e := repository.Article(ctx, tx, id)
		if e != nil {
			return nil, 0, e
		}
		if b.ExpectedVersion != v.Version {
			return nil, 0, domain.E("VERSION_CONFLICT")
		}
		if v.Archived && !b.Archived {
			if e = kbCap(ctx, tx); e != nil {
				return nil, 0, e
			}
		}
		tags := b.Tags
		if tags == nil {
			tags = []string{}
		}
		changed := b.Title != v.Title || b.Body != v.Body || repository.JSON(tags) != repository.JSON(v.Tags) || b.Archived != v.Archived
		if changed {
			_, e = tx.ExecContext(ctx, "UPDATE knowledge_articles SET title=?,body=?,tags_json=?,archived=?,version=version+1,updated_at=? WHERE id=?", b.Title, b.Body, repository.JSON(tags), b.Archived, repository.Now(), id)
			if e != nil {
				return nil, 0, e
			}
		}
		if e = s.audit(ctx, tx, a, nil, "knowledge_updated", map[string]any{"article_id": id, "changed": changed}); e != nil {
			return nil, 0, e
		}
		v, e = repository.Article(ctx, tx, id)
		return v, 200, e
	})
}
