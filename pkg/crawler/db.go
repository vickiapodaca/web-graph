package crawler

import (
	"database/sql"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"sync"
)

// Storage implements a PostgreSQL storage backend for colly
type Storage struct {
	URI       string
	PageTable string
	LinkTable string
	db        *sql.DB
	linkLock  *sync.RWMutex
	pageLock  *sync.RWMutex
}

// Init initializes the PostgreSQL storage
func (s *Storage) Init() error {

	var err error

	if s.linkLock == nil {
		s.linkLock = &sync.RWMutex{}
	}

	if s.pageLock == nil {
		s.pageLock = &sync.RWMutex{}
	}

	if s.db, err = sql.Open("postgres", s.URI); err != nil {
		return err
	}

	if err = s.db.Ping(); err != nil {
		return err
	}

	query := fmt.Sprintf(`CREATE TABLE IF NOT EXISTS %s (
		page_id text NOT NULL PRIMARY KEY UNIQUE, 
		host text NOT NULL, 
		path text NOT NULL, 
		url text NOT NULL
		);`, s.PageTable)

	if _, err = s.db.Exec(query); err != nil {
		return err
	}

	query = fmt.Sprintf(`CREATE TABLE IF NOT EXISTS %s (
		from_page_id text NOT NULL, 
		to_page_id text NOT NULL, 
		text text, 
		type text NOT NULL, 
		CONSTRAINT PK_Link PRIMARY KEY (from_page_id,to_page_id),
		CONSTRAINT FK_from_page_id FOREIGN KEY (from_page_id) REFERENCES %s(page_id),
		CONSTRAINT FK_to_page_id FOREIGN KEY (to_page_id) REFERENCES %s(page_id)
		);`, s.LinkTable, s.PageTable, s.PageTable)

	if _, err = s.db.Exec(query); err != nil {
		return err
	}

	return nil

}

// CheckPageExists checks that the page exists in the visited database
func (s *Storage) CheckPageExists(u *url.URL) (bool, error) {
	var isVisited bool

	query := fmt.Sprintf(`SELECT EXISTS(SELECT page_id FROM %s WHERE page_id = $1)`, s.PageTable)

	s.pageLock.RLock()
	err := s.db.QueryRow(query, Hash(u)).Scan(&isVisited)
	s.pageLock.RUnlock()
	return isVisited, err
}

// AddPage first checks that it does not exist, and then inserts the page
func (s *Storage) AddPage(u *url.URL) error {
	visited, err := s.CheckPageExists(u)
	if err != nil {
		return err
	}

	if visited {
		return nil
	}

	query := fmt.Sprintf(`INSERT INTO %s (page_id, host, path, url) VALUES($1, $2, $3, $4);`, s.PageTable)

	s.pageLock.Lock()
	_, err = s.db.Exec(query, Hash(u), u.Hostname(), u.EscapedPath(), u.String())
	s.pageLock.Unlock()
	return err
}

// CheckLinkExists checks that the link exists in the visited database
func (s *Storage) CheckLinkExists(fromU *url.URL, toU *url.URL) (bool, error) {
	var isVisited bool

	query := fmt.Sprintf(`SELECT EXISTS(SELECT to_page_id FROM %s WHERE from_page_id = $1 AND to_page_id = $2)`, s.LinkTable)

	// s.linkLock.RLock()
	err := s.db.QueryRow(query, Hash(fromU), Hash(toU)).Scan(&isVisited)
	// s.linkLock.RUnlock()
	return isVisited, err
}

// AddLink first checks that it does not exist, and then inserts the page
func (s *Storage) AddLink(fromU *url.URL, toU *url.URL, linkText string, linkType string) error {
	s.linkLock.Lock()
	defer s.linkLock.Unlock()
	// First, check the link already exists
	visited, err := s.CheckLinkExists(fromU, toU)
	if err != nil {
		return err
	}

	if visited {
		return nil
	}

	// Then try to add the pages
	s.AddPage(fromU)
	s.AddPage(toU)

	query := fmt.Sprintf(`INSERT INTO %s (from_page_id, to_page_id, text, type) VALUES($1, $2, $3, $4);`, s.LinkTable)

	_, err = s.db.Exec(query, Hash(fromU), Hash(toU), linkText, linkType)
	return err
}

// BatchAddLinks takes a batch of links and inserts them, not giving a fuck whether or not they clash
func (s *Storage) BatchAddLinks(links []*Link) error {
	// Hmmm, not sure what to do about this page bullshit, maybe I'll make a batch process for that too
	// // Then try to add the pages
	// s.AddPage(fromU)
	// s.AddPage(toU)

	sqlStr := fmt.Sprintf("INSERT INTO %s (from_page_id, to_page_id, text, type) VALUES ", s.LinkTable)
	vals := []interface{}{}

	for _, link := range links {
		sqlStr += "(?, ?, ?, ?),"
		vals = append(vals, Hash(link.FromU), Hash(link.ToU), link.LinkText, link.LinkType)
	}

	//trim the last ,
	sqlStr = strings.TrimSuffix(sqlStr, ",")

	// Add "fuck it, idc" to the end
	sqlStr += " ON CONFLICT DO NOTHING"

	//Replacing ? with $n for postgres
	sqlStr = ReplaceSQL(sqlStr, "?")

	//prepare the statement
	stmt, _ := s.db.Prepare(sqlStr)
	defer stmt.Close()

	//format all vals at once
	_, err := stmt.Exec(vals...)

	return err
}

// BatchAddPages takes a batch of pages and inserts them, not giving a fuck whether or not they clash
func (s *Storage) BatchAddPages(pages []*Page) error {
	sqlStr := fmt.Sprintf("INSERT INTO %s (page_id, host, path, url) VALUES ", s.PageTable)
	vals := []interface{}{}

	for _, page := range pages {
		sqlStr += "(?, ?, ?, ?),"
		vals = append(vals, Hash(page.U), page.U.Hostname(), page.U.EscapedPath(), page.U.String())
	}

	//trim the last ,
	sqlStr = strings.TrimSuffix(sqlStr, ",")

	// Add "fuck it, idc" to the end
	sqlStr += " ON CONFLICT DO NOTHING"

	//Replacing ? with $n for postgres
	sqlStr = ReplaceSQL(sqlStr, "?")

	//prepare the statement
	stmt, _ := s.db.Prepare(sqlStr)
	defer stmt.Close()

	//format all vals at once
	_, err := stmt.Exec(vals...)

	return err
}

// ReplaceSQL replaces the instance occurrence of any string pattern with an increasing $n based sequence
func ReplaceSQL(old, searchPattern string) string {
	tmpCount := strings.Count(old, searchPattern)
	for m := 1; m <= tmpCount; m++ {
		old = strings.Replace(old, searchPattern, "$"+strconv.Itoa(m), 1)
	}
	return old
}
