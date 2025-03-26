package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"reading-list-api/internal/types"
	"regexp"
	"strconv"
	"strings" // <-- Add this import
	"time"

	"github.com/PuerkitoBio/goquery" // <-- Add this import
	"github.com/go-chi/render"
	"google.golang.org/genai"
)

// ExtractedContent holds the structured data extracted locally from the URL
type ExtractedContent struct {
	URL           string
	Title         string
	Author        string
	DatePublished string // Store as string for flexibility
	BodyText      string
	// Flags to track if we found the metadata locally
	TitleFound  bool
	AuthorFound bool
	DateFound   bool
}

// fetchAndExtractLocally attempts to extract metadata and main body text using goquery.
func fetchAndExtractLocally(ctx context.Context, url string) (*ExtractedContent, error) {
	content := &ExtractedContent{URL: url} // Initialize with URL
	log.Printf("Starting local extraction for: %s", url)

	// Create a request with context
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, fmt.Errorf("error creating request for %s: %w", url, err)
	}
	// Add a user-agent, some sites block default Go client
	req.Header.Set("User-Agent", "ReadingListBot/1.0 (+https://yourdomain.com/botinfo)") // Optional: Be polite

	// 1. Make HTTP request using default client (consider timeout)
	client := &http.Client{Timeout: 30 * time.Second} // Add a timeout
	res, err := client.Do(req)
	if err != nil {
		// Check for context deadline exceeded
		if errors.Is(err, context.DeadlineExceeded) {
			log.Printf("Timeout fetching URL %s", url)
			return nil, fmt.Errorf("timeout fetching URL %s: %w", url, err)
		}
		return nil, fmt.Errorf("error fetching URL %s: %w", url, err)
	}
	defer res.Body.Close()

	if res.StatusCode != http.StatusOK {
		log.Printf("Warning: Unexpected status code %d for URL %s", res.StatusCode, url)
		// Don't necessarily fail here, maybe Gemini can still access it, but log it.
		// We'll return the partially filled content struct.
		return content, nil // Return empty content, let Gemini try fully
		// Or return fmt.Errorf("unexpected status code %d for URL %s", res.StatusCode, url) if you want to fail hard
	}

	// 2. Parse HTML
	doc, err := goquery.NewDocumentFromReader(res.Body)
	if err != nil {
		// Log the error but proceed, maybe Gemini can still parse it
		log.Printf("Warning: Error parsing HTML from %s: %v. Proceeding without local extraction.", url, err)
		return content, nil // Return empty content
	}
	log.Printf("HTML parsed successfully for: %s", url)

	// --- 3. Extract Metadata (using goquery) ---
	// Title
	content.Title = strings.TrimSpace(doc.Find("head title").First().Text())
	if content.Title == "" {
		content.Title, _ = doc.Find("head meta[property='og:title']").Attr("content")
		content.Title = strings.TrimSpace(content.Title)
	}
	if content.Title == "" {
		content.Title, _ = doc.Find("head meta[name='twitter:title']").Attr("content")
		content.Title = strings.TrimSpace(content.Title)
	}
	if content.Title == "" {
		content.Title = strings.TrimSpace(doc.Find("h1").First().Text())
	}
	content.TitleFound = content.Title != ""
	if content.TitleFound {
		log.Printf("Local extraction found Title: %.50s...", content.Title)
	}

	// --- Author Extraction (Expanded Logic) ---
	log.Printf("Attempting local author extraction for: %s", url)
	var authors []string // Use a slice to potentially capture multiple authors

	// Method 1: Specific Meta Tags (Highest Priority after JSON-LD which is checked later)
	metaAuthor, metaExists := doc.Find("head meta[name='author']").Attr("content")
	if metaExists && strings.TrimSpace(metaAuthor) != "" {
		authors = append(authors, strings.TrimSpace(metaAuthor))
		log.Printf("Author found via meta[name='author']: %s", authors[0])
	}

	if len(authors) == 0 {
		metaAuthor, metaExists = doc.Find("head meta[property='article:author']").Attr("content")
		if metaExists && strings.TrimSpace(metaAuthor) != "" {
			// This meta tag can sometimes contain a URL, sometimes a name. Handle URLs if needed later.
			authors = append(authors, strings.TrimSpace(metaAuthor))
			log.Printf("Author found via meta[property='article:author']: %s", authors[0])
		}
	}

	if len(authors) == 0 {
		metaAuthor, metaExists = doc.Find("head meta[name='dc.creator']").Attr("content") // Dublin Core
		if metaExists && strings.TrimSpace(metaAuthor) != "" {
			authors = append(authors, strings.TrimSpace(metaAuthor))
			log.Printf("Author found via meta[name='dc.creator']: %s", authors[0])
		}
	}

	// Method 2: Schema.org Microdata (itemprop) - Often in <span>
	if len(authors) == 0 {
		doc.Find("[itemprop='author'], [itemprop='creator']").Each(func(i int, s *goquery.Selection) {
			var authorName string
			// Check if it's a meta tag inside, or the element itself has the name
			if metaContent, metaExists := s.Find("meta[itemprop='name']").Attr("content"); metaExists && strings.TrimSpace(metaContent) != "" {
				authorName = strings.TrimSpace(metaContent)
			} else if metaContent, metaExists := s.Attr("content"); metaExists && goquery.NodeName(s) == "meta" && strings.TrimSpace(metaContent) != "" {
				// Handle cases where itemprop=author is on a <meta content="Name"> tag
				authorName = strings.TrimSpace(metaContent)
			} else if s.Is("span, div, a, p") { // Check common tags directly
				authorName = strings.TrimSpace(s.Text())
			}

			if authorName != "" && !contains(authors, authorName) { // Avoid duplicates
				authors = append(authors, authorName)
				log.Printf("Author found via itemprop on %s: %s", goquery.NodeName(s), authorName)
			}
		})
	}

	// Method 3: Heuristics - Common Classes and Attributes within specific sections
	if len(authors) == 0 {
		// Define common metadata container selectors
		metaContainers := []string{".byline", ".post-meta", ".entry-meta", "header .meta", ".author-info", ".article-author"}
		containerSelector := strings.Join(metaContainers, ", ")

		doc.Find(containerSelector).Each(func(i int, metaSection *goquery.Selection) {
			// Look for specific elements within these containers
			metaSection.Find(".author, .author-name, .writer, .contributor, [rel='author'], a[href*='/author/'], a[href*='/profile/']").Each(func(j int, authorElement *goquery.Selection) {
				authorName := strings.TrimSpace(authorElement.Text())
				// Basic filter: ignore common junk text often found in links
				lowerName := strings.ToLower(authorName)
				if authorName != "" && len(authorName) > 1 && lowerName != "author profile" && lowerName != "profile" && !contains(authors, authorName) {
					authors = append(authors, authorName)
					log.Printf("Author found via heuristic (%s) in container (%d): %s", authorElement.Nodes[0].Data, i, authorName)
				}
			})
		})
	}

	// Method 4: Fallback Heuristic - Generic ".author" class anywhere (less reliable)
	if len(authors) == 0 {
		doc.Find(".author").Each(func(i int, s *goquery.Selection) {
			// Avoid picking up comments sections etc. Check context if possible.
			// Simple check: only take if text is reasonably short like a name
			authorName := strings.TrimSpace(s.Text())
			if authorName != "" && len(authorName) < 50 && !contains(authors, authorName) { // Arbitrary length limit
				// Maybe check if parent is NOT a comment section? Complex.
				authors = append(authors, authorName)
				log.Printf("Author found via generic .author class (fallback): %s", authorName)
			}
		})
	}

	// Combine found authors
	if len(authors) > 0 {
		content.Author = strings.Join(authors, ", ") // Join multiple authors with comma
		content.AuthorFound = true
		log.Printf("Final local authors extracted: %s", content.Author)
	} else {
		log.Printf("Local author extraction failed for: %s", url)
	}

	// --- JSON-LD Check (Enhanced Author part) ---
	doc.Find("script[type='application/ld+json']").Each(func(i int, s *goquery.Selection) {
		var jsonData map[string]interface{}
		if err := json.Unmarshal([]byte(s.Text()), &jsonData); err == nil {
			if typ, ok := jsonData["@type"]; ok {
				// Check if type is string or []string/[]interface{}
				isArticle := false
				if typeStr, okStr := typ.(string); okStr && (typeStr == "Article" || typeStr == "NewsArticle" || typeStr == "BlogPosting") {
					isArticle = true
				} else if typeArr, okArr := typ.([]interface{}); okArr {
					for _, t := range typeArr {
						if ts, okTs := t.(string); okTs && (ts == "Article" || ts == "NewsArticle" || ts == "BlogPosting") {
							isArticle = true
							break
						}
					}
				}

				if isArticle {
					log.Printf("Found JSON-LD Article structure for %s", url)
					// If metadata wasn't found above, try extracting from JSON-LD
					// This now acts as a high-priority override/fill-in if found
					if authorData, ok := jsonData["author"]; ok {
						var jsonAuthors []string
						// Case 1: Author is a single object {"@type": "Person", "name": "..."}
						if authorObj, okObj := authorData.(map[string]interface{}); okObj {
							if name, okName := authorObj["name"].(string); okName && name != "" {
								jsonAuthors = append(jsonAuthors, name)
							}
						} else if authorArr, okArr := authorData.([]interface{}); okArr {
							// Case 2: Author is an array of objects [ {..}, {..} ]
							for _, item := range authorArr {
								if authorObj, okObj := item.(map[string]interface{}); okObj {
									if name, okName := authorObj["name"].(string); okName && name != "" {
										if !contains(jsonAuthors, name) { // Avoid duplicates within JSON array
											jsonAuthors = append(jsonAuthors, name)
										}
									}
								}
							}
						}
						if len(jsonAuthors) > 0 {
							newAuthorString := strings.Join(jsonAuthors, ", ")
							if !content.AuthorFound || content.Author != newAuthorString { // Update if not found or different
								log.Printf("Author updated/found via JSON-LD: %s", newAuthorString)
								content.Author = newAuthorString
								content.AuthorFound = true
							}
						}
					}
					// ... (JSON-LD checks for Date and Title remain the same, potentially updating if found) ...
					if !content.DateFound {
						if dateData, ok := jsonData["datePublished"].(string); ok && dateData != "" {
							t, err := time.Parse(time.RFC3339, dateData)
							if err == nil {
								newDate := t.Format("2006-01-02")
								if !content.DateFound || content.DatePublished != newDate {
									log.Printf("Date updated/found via JSON-LD: %s", newDate)
									content.DatePublished = newDate
									content.DateFound = true
								}
							} else {
								// Handle potentially different date formats in JSON-LD if needed
								log.Printf("WARN: Could not parse JSON-LD date '%s' with RFC3339: %v", dateData, err)
							}
						}
					}
					if !content.TitleFound {
						if headline, ok := jsonData["headline"].(string); ok && headline != "" {
							if !content.TitleFound || content.Title != headline {
								log.Printf("Title updated/found via JSON-LD: %.50s...", headline)
								content.Title = headline
								content.TitleFound = true
							}
						}
					}
				}
			}
		}
	})

	var potentialDateStrings []string

	// Collect potential date strings from various sources
	if dateStr, exists := doc.Find("head meta[property='article:published_time']").Attr("content"); exists {
		potentialDateStrings = append(potentialDateStrings, strings.TrimSpace(dateStr))
	}
	if dateStr, exists := doc.Find("head meta[name='date']").Attr("content"); exists {
		potentialDateStrings = append(potentialDateStrings, strings.TrimSpace(dateStr))
	}
	if dateStr, exists := doc.Find("head meta[name='dc.date']").Attr("content"); exists { // Dublin Core date
		potentialDateStrings = append(potentialDateStrings, strings.TrimSpace(dateStr))
	}
	if dateStr, exists := doc.Find("time[itemprop='datePublished'], time[datetime]").First().Attr("datetime"); exists {
		potentialDateStrings = append(potentialDateStrings, strings.TrimSpace(dateStr))
	}
	// Add visible text last as it's less likely to have precise timezone info
	potentialDateStrings = append(potentialDateStrings, strings.TrimSpace(doc.Find(".post-date, .publish-date, .entry-date, [itemprop='datePublished']").First().Text()))

	// Try parsing the potential date strings using the helper
	for _, dateStr := range potentialDateStrings {
		formattedDate, success := parseAndFormatDateToUTC(dateStr)
		if success {
			// Use the first successfully parsed and formatted date
			if !content.DateFound || content.DatePublished != formattedDate { // Update if not found or different
				log.Printf("Local extraction found and formatted Date: %s (from source string '%s')", formattedDate, dateStr)
				content.DatePublished = formattedDate
				content.DateFound = true
			}
			break // Stop after finding the first valid date
		}
	}

	if !content.DateFound {
		log.Printf("Local date extraction failed or couldn't parse found strings for: %s", url)
	}

	// --- JSON-LD Check (Using Helper for Date) ---
	doc.Find("script[type='application/ld+json']").Each(func(i int, s *goquery.Selection) {
		var jsonData map[string]interface{}
		if err := json.Unmarshal([]byte(s.Text()), &jsonData); err == nil {
			// ... (Type checking and Author extraction remain the same) ...
			isArticle := false // Assume type checking logic is here...
			// ...
			if typ, ok := jsonData["@type"].(string); ok && (typ == "Article" || typ == "NewsArticle" || typ == "BlogPosting") { // Simplified check
				isArticle = true
			} // Add array check if needed

			if isArticle {
				// ... (Author extraction from JSON-LD) ...

				// Date extraction from JSON-LD using the helper
				if dateData, ok := jsonData["datePublished"].(string); ok && dateData != "" {
					formattedDate, success := parseAndFormatDateToUTC(dateData)
					if success {
						// Update if JSON-LD provides a valid date and it wasn't found before or is different
						if !content.DateFound || content.DatePublished != formattedDate {
							log.Printf("Date updated/found via JSON-LD: %s", formattedDate)
							content.DatePublished = formattedDate
							content.DateFound = true
						}
					} else {
						log.Printf("WARN: Failed to parse JSON-LD date '%s' using helper.", dateData)
					}
				}
				// ... (Title extraction from JSON-LD) ...
			}
		}
	})

	// --- 4. Extract Main Body Text ---
	var textBuilder strings.Builder
	selector := "article, main, .post-content, .entry-content, .td-post-content, #content, #main" // Add more selectors if needed
	mainContent := doc.Find(selector).First()

	if mainContent.Length() > 0 {
		mainContent.Find("p, h1, h2, h3, h4, h5, h6, li, blockquote, pre").Each(func(i int, s *goquery.Selection) {
			if goquery.NodeName(s) != "script" && goquery.NodeName(s) != "style" {
				trimmedText := strings.TrimSpace(s.Text())
				// Basic filtering (optional): skip very short lines unless it's a heading
				isHeading := strings.HasPrefix(goquery.NodeName(s), "h")
				if len(trimmedText) > 20 || isHeading || goquery.NodeName(s) == "li" { // Adjust threshold
					textBuilder.WriteString(trimmedText)
					textBuilder.WriteString("\n\n")
				}
			}
		})
	}
	content.BodyText = textBuilder.String()

	// Fallback if primary extraction failed
	if content.BodyText == "" {
		log.Printf("Warning: Main content extraction failed for %s. Falling back to simple body p extraction.", url)
		textBuilder.Reset()
		doc.Find("body p").Each(func(i int, s *goquery.Selection) {
			paragraphText := strings.TrimSpace(s.Text())
			if len(paragraphText) > 20 { // Filter short paragraphs too
				textBuilder.WriteString(paragraphText)
				textBuilder.WriteString("\n\n")
			}
		})
		content.BodyText = textBuilder.String()
	}
	if content.BodyText != "" {
		log.Printf("Local extraction found Body Text (length: %d)", len(content.BodyText))
	} else {
		log.Printf("Warning: Could not extract any meaningful body text locally for %s", url)
	}

	return content, nil
}

type ArticleResponse struct {
	*types.Article
}

func (s *Server) GetArticlesHandler(w http.ResponseWriter, r *http.Request) {
	// 1 - query sqlite db for all articles
	articles, err := s.db.GetAllArticles()
	if err != nil {
		render.Render(w, r, ErrInternalServer(err))
		return
	}
	// 2 - return list of articles as a response
	err = render.RenderList(w, r, NewArticleListResponse(articles))
	if err != nil {
		render.Render(w, r, ErrRender(err))
		return
	}
}

func NewArticleListResponse(articles *[]types.Article) []render.Renderer {
	list := []render.Renderer{}

	for _, article := range *articles {
		list = append(list, NewArticleResponse(&article))
	}
	return list
}

func NewArticleResponse(article *types.Article) *ArticleResponse {
	resp := &ArticleResponse{Article: article}
	return resp
}

func (rd *ArticleResponse) Render(w http.ResponseWriter, r *http.Request) error {
	return nil
}

func (s *Server) CreateArticle(w http.ResponseWriter, r *http.Request) {
	// 1 - extract the article link from the request body
	data := &ArticleRequest{}
	if err := render.Bind(r, data); err != nil {
		render.Render(w, r, ErrInvalidRequest(err))
		return
	}
	articleLink := data.ArticleLink
	log.Printf("Received request to create article for link: %s", articleLink)

	// 2 - check if the link already exists in the db
	exists, err := s.db.ArticleExists(articleLink)
	if err != nil {
		log.Printf("ERROR: DB check failed for %s: %v", articleLink, err)
		render.Render(w, r, ErrInternalServer(err))
		return
	}
	if exists {
		log.Printf("INFO: Article link %s already exists in DB.", articleLink)
		render.Render(w, r, ErrInvalidRequest(fmt.Errorf("article already exists"))) // More specific error
		return
	}

	// 3a - Attempt local extraction first
	// Create a context with a timeout for the entire operation
	// Adjust timeout as needed, considering both local fetch and Gemini call
	ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
	defer cancel()

	localContent, localErr := fetchAndExtractLocally(ctx, articleLink)
	if localErr != nil {
		// Log the error but proceed; Gemini might still work if it was just local fetch/parse issue
		log.Printf("WARNING: Local extraction failed for %s: %v. Proceeding to Gemini.", articleLink, localErr)
		// Ensure localContent is not nil for the Gemini call
		if localContent == nil {
			localContent = &ExtractedContent{URL: articleLink} // Create empty struct if fetch failed badly
		}
	}

	// 3b - Get metadata using Gemini, potentially filling gaps found by local extraction
	var geminiResult *GeminiArticleDetails
	var geminiErr error
	numRetries, _ := strconv.Atoi(os.Getenv("NUM_RETRIES")) // Default to 0 if env var not set or invalid
	if numRetries <= 0 {
		numRetries = 1 // Ensure at least one attempt
	}

	log.Printf("Attempting Gemini extraction for %s (Max Retries: %d)", articleLink, numRetries)
	for attempt := 0; attempt < numRetries; attempt++ {
		if ctx.Err() != nil { // Check if context timeout exceeded before next attempt
			log.Printf("ERROR: Context deadline exceeded before Gemini attempt %d for %s", attempt+1, articleLink)
			render.Render(w, r, ErrInternalServer(fmt.Errorf("processing timed out")))
			return
		}
		log.Printf("Gemini attempt %d/%d for %s", attempt+1, numRetries, articleLink)
		geminiResult, geminiErr = extractArticleMetadata(ctx, articleLink, localContent)

		// Check for successful result AND valid content type
		if geminiErr == nil && geminiResult != nil && geminiResult.Type >= 0 {
			log.Printf("Gemini extraction successful on attempt %d for %s", attempt+1, articleLink)
			break // Success!
		}

		// Handle specific non-retryable error from Gemini (e.g., invalid content type)
		if geminiErr == nil && geminiResult != nil && geminiResult.Type == -1 {
			log.Printf("INFO: Gemini classified %s as invalid type (-1) on attempt %d.", articleLink, attempt+1)
			render.Render(w, r, ErrInvalidRequest(fmt.Errorf("link supplied is not a supported article/book/paper or is inaccessible")))
			return
		}

		// Log the error for this attempt
		if geminiErr != nil {
			log.Printf("WARNING: Gemini attempt %d failed for %s: %v", attempt+1, articleLink, geminiErr)
		} else if geminiResult == nil {
			log.Printf("WARNING: Gemini attempt %d returned nil result for %s", attempt+1, articleLink)
		} else {
			// This case shouldn't happen if Type >= 0 or Type == -1 checks passed
			log.Printf("WARNING: Gemini attempt %d had unexpected state for %s: Type=%d", attempt+1, articleLink, geminiResult.Type)
		}

		// If it's the last attempt and still failed
		if attempt == numRetries-1 {
			log.Printf("ERROR: Gemini extraction failed after %d attempts for %s", numRetries, articleLink)
			render.Render(w, r, ErrInternalServer(fmt.Errorf("failed to extract article metadata after %d attempts: %w", numRetries, geminiErr))) // Return the last error
			return
		}

		// Wait before retrying (optional, simple backoff)
		time.Sleep(time.Duration(attempt+1) * 1 * time.Second) // Simple linear backoff
	}

	// 4 - Combine results and create the final Article object
	// Prioritize Gemini's findings for metadata, use local for body
	article := &types.Article{
		Title:         geminiResult.Title,         // From Gemini (verified/found)
		Author:        geminiResult.Author,        // From Gemini (verified/found)
		Summary:       geminiResult.Summary,       // From Gemini
		DatePublished: geminiResult.DatePublished, // From Gemini (verified/found)
		Type:          geminiResult.Type,          // From Gemini
		// Body:          localContent.BodyText,           // From Local Extraction
		DateRead: time.Now().Format("2006-01-02"), // Current date
		Link:     articleLink,                     // Original Link
	}

	// Basic validation on combined result (ensure title/summary aren't empty)
	if article.Title == "" || article.Summary == "" {
		log.Printf("ERROR: Final article data incomplete for %s (Title: '%s', Summary: '%s')", articleLink, article.Title, article.Summary)
		render.Render(w, r, ErrInternalServer(fmt.Errorf("failed to obtain complete article details (missing title or summary)")))
		return
	}

	// 5 - Insert into DB
	log.Printf("Inserting article into DB: %s", article.Title)
	err = s.db.InsertArticle(article)
	if err != nil {
		log.Printf("ERROR: Failed to insert article %s into DB: %v", articleLink, err)
		render.Render(w, r, ErrInternalServer(err))
		return
	}
	log.Printf("Successfully inserted article: %s", article.Title)

	// 6 - Return created article
	render.Status(r, http.StatusCreated) // Set 201 status for creation
	if err := render.Render(w, r, NewArticleResponse(article)); err != nil {
		log.Printf("ERROR: Failed to render response for article %s: %v", articleLink, err)
		render.Render(w, r, ErrRender(err))
		return
	}
}

type ArticleRequest struct {
	ArticleLink string `json:"articleLink"`
}

func (a *ArticleRequest) Bind(r *http.Request) error {
	if a.ArticleLink == "" {
		return errors.New("missing required Article fields")
	}

	return nil
}

func extractArticleMetadata(ctx context.Context, articleLink string, localContent *ExtractedContent) (*GeminiArticleDetails, error) {
	client, err := genai.NewClient(ctx, &genai.ClientConfig{
		APIKey:  os.Getenv("GEMINI_API_KEY"),
		Backend: genai.BackendGeminiAPI,
	})
	if err != nil {
		log.Printf("ERROR: Could not connect to gemini: %v", err)
		return nil, err
	}

	// --- Dynamic Prompt Construction (Same as before) ---
	// ... (prompt building logic) ...
	var promptBuilder strings.Builder
	promptBuilder.WriteString(fmt.Sprintf("You are an expert assistant tasked with extracting metadata and summarizing web content. Analyze the content found at the following URL: %s\n\n", articleLink))
	promptBuilder.WriteString("I attempted to extract some information locally. Here's what I found (use this as context, but verify and prioritize information directly from the source URL using search if necessary):\n")
	if localContent.TitleFound {
		promptBuilder.WriteString(fmt.Sprintf("- Locally Found Title: %s\n", localContent.Title))
	} else {
		promptBuilder.WriteString("- Locally Found Title: (Not found)\n")
	}
	if localContent.AuthorFound {
		promptBuilder.WriteString(fmt.Sprintf("- Locally Found Author: %s\n", localContent.Author))
	} else {
		promptBuilder.WriteString("- Locally Found Author: (Not found) - Please use Google Search to find the author(s).\n")
	}
	if localContent.DateFound {
		promptBuilder.WriteString(fmt.Sprintf("- Locally Found Published Date: %s (Please prioritize using this exact date if it appears valid)\n", localContent.DatePublished))
	} else {
		promptBuilder.WriteString("- Locally Found Published Date: (Not found) - Please use Google Search to find the publication date.\n")
	}
	if localContent.BodyText != "" {
		promptBuilder.WriteString("\nI also extracted the following body text locally. Use this text primarily for the summary, but refer to the original URL via search if needed for completeness or context:\n---\n")
		maxBodyLen := 8000
		if len(localContent.BodyText) > maxBodyLen {
			promptBuilder.WriteString(localContent.BodyText[:maxBodyLen])
			promptBuilder.WriteString("\n[Body text truncated due to length limit for this prompt]...")
		} else {
			promptBuilder.WriteString(localContent.BodyText)
		}
		promptBuilder.WriteString("\n---\n")
	} else {
		promptBuilder.WriteString("\nWarning: I could not extract body text locally. You will need to retrieve the full content from the URL using search to perform the summary.\n")
	}
	promptBuilder.WriteString("\nBased on your analysis of the URL (using Google Search as needed, especially if local information is missing or questionable):")
	promptBuilder.WriteString("\n1. Verify or find the definitive Title.")
	promptBuilder.WriteString("\n2. Verify or find the Author(s).")
	promptBuilder.WriteString("\n3. Verify or find the Publication Date (format as YYYY-MM-DD if possible, otherwise provide what you find).")
	promptBuilder.WriteString("\n4. Generate a concise, single-sentence Summary of the main topic/argument.")
	promptBuilder.WriteString("\n5. Determine the content Type.")
	promptBuilder.WriteString("\n\nPlease provide your findings strictly in the following JSON format, enclosed in ```json ... ```:\n")
	promptBuilder.WriteString("```json\n")
	promptBuilder.WriteString("{\n")
	promptBuilder.WriteString(`  "title": "(The definitive full title)",` + "\n")
	promptBuilder.WriteString(`  "author": "(The author(s), comma-separated if multiple, or empty string if truly none found)",` + "\n")
	promptBuilder.WriteString(`  "summary": "(Concise single-sentence summary)",` + "\n")
	promptBuilder.WriteString(`  "datePublished": "(YYYY-MM-DD or best available date string, or empty string)",` + "\n")
	promptBuilder.WriteString(`  "type": "(0 for article, 1 for academic/research paper, 2 for book, -1 if none of these or content is inaccessible/paywalled)"` + "\n")
	promptBuilder.WriteString("}\n")
	promptBuilder.WriteString("```")
	finalPrompt := promptBuilder.String()
	log.Println("Final prompt", finalPrompt)
	// --- Gemini API Call ---
	toolConfig := &genai.GenerateContentConfig{
		Tools: []*genai.Tool{
			{
				GoogleSearch: &genai.GoogleSearch{},
			},
		},
	}
	content := genai.Text(finalPrompt)
	modelName := "gemini-2.5-pro-exp-03-25"
	log.Printf("Sending prompt to Gemini (stream) model %s for URL: %s", modelName, articleLink)
	iter := client.Models.GenerateContentStream(
		ctx,
		modelName,
		content,
		toolConfig,
	)

	// --- Process Gemini Response Stream using range (Simplified error check) ---
	var geminiResponseBuilder strings.Builder
	for resp, err := range iter {
		// Check for any error during iteration
		if err != nil {
			// Check specifically for context cancellation
			// Use errors.Is for potentially wrapped context errors
			if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				log.Printf("ERROR: Context cancelled during Gemini stream for %s: %v", articleLink, err)
				return nil, err // Return the context error directly
			}
			// Any other error is a stream failure
			log.Printf("ERROR: Gemini stream iteration failed for %s: %v", articleLink, err)
			return nil, fmt.Errorf("gemini stream iteration error: %w", err)
		}

		// Process valid response (err is nil)
		if resp != nil {
			if len(resp.Candidates) > 0 && len(resp.Candidates[0].Content.Parts) > 0 {
				geminiResponseBuilder.WriteString(resp.Text())
			} else {
				log.Printf("WARN: Gemini stream response part empty or candidate missing for %s", articleLink)
			}
		} else {
			// This case (err == nil && resp == nil) shouldn't happen with range over Seq2
			log.Printf("WARN: Gemini stream returned nil response and nil error for %s (unexpected)", articleLink)
		}
	} // End of range loop

	// Final context check after loop (optional but good practice)
	if ctx.Err() != nil {
		log.Printf("ERROR: Context cancelled possibly after Gemini stream finished for %s: %v", articleLink, ctx.Err())
		return nil, fmt.Errorf("context cancelled possibly after stream completion: %w", ctx.Err())
	}

	geminiFullContent := geminiResponseBuilder.String()
	if geminiFullContent == "" {
		log.Printf("ERROR: Gemini stream returned no text content for %s", articleLink)
		return nil, errors.New("gemini returned empty response from stream (check logs for iteration errors)")
	}
	log.Printf("DEBUG: Gemini Full Stream Response: %s", geminiFullContent)

	// --- Extract and Parse JSON (Same Regex as before) ---
	re := regexp.MustCompile(`(?m)^(?s){(.*)}$`)
	cleanedString := re.FindString(geminiFullContent)
	if cleanedString == "" {
		return nil, fmt.Errorf("could not parse gemini content with regex")
	}
	log.Printf("DEBUG: Extracted JSON string for unmarshalling: %s", cleanedString) // Add for confirmation

	var geminiArticleMetadata GeminiArticleDetails
	err = json.Unmarshal([]byte(cleanedString), &geminiArticleMetadata)
	if err != nil {
		// Log the actual string that failed to unmarshal
		log.Printf("ERROR: Failed to unmarshal Gemini JSON for %s: %v. JSON string was: %s", articleLink, err, cleanedString)
		return nil, fmt.Errorf("error unmarshalling gemini json: %w", err)
	}

	log.Printf("Successfully extracted metadata from Gemini stream for %s", articleLink)
	return &geminiArticleMetadata, nil
}

// generate content stream investigate
type GeminiArticleDetails struct {
	Title         string `json:"title"`
	Author        string `json:"author"`
	Summary       string `json:"summary"`
	DatePublished string `json:"datePublished"`
	Type          int    `json:"type"`
}

// Helper function to check if a slice of strings contains a string
func contains(slice []string, item string) bool {
	for _, a := range slice {
		if a == item {
			return true
		}
	}
	return false
}

func parseAndFormatDateToUTC(dateStr string) (string, bool) {
	if dateStr == "" {
		return "", false
	}

	// Define common layouts, including those with timezones
	layouts := []string{
		time.RFC3339,                // "2006-01-02T15:04:05Z07:00" (Most common for meta/JSON-LD)
		time.RFC3339Nano,            // Includes nanoseconds
		"2006-01-02T15:04:05",       // ISO 8601 without timezone (assume UTC or parse relative?) - Less common for pub dates
		"2006-01-02 15:04:05 Z0700", // Another common format
		"2006-01-02 15:04:05 Z07:00",
		"2006-01-02",      // Just the date
		"02 Jan 2006",     // Common textual format
		"January 2, 2006", // Full month name
		time.RFC1123Z,     // "Mon, 02 Jan 2006 15:04:05 -0700"
		time.RFC1123,      // "Mon, 02 Jan 2006 15:04:05 MST"
		time.RFC822Z,      // "02 Jan 06 15:04 -0700"
		time.RFC822,       // "02 Jan 06 15:04 MST"
		// Add more layouts if you encounter other common formats
	}

	var parsedTime time.Time
	var parseSuccess bool

	trimmedDateStr := strings.TrimSpace(dateStr)

	for _, layout := range layouts {
		t, err := time.Parse(layout, trimmedDateStr)
		if err == nil {
			parsedTime = t
			parseSuccess = true
			log.Printf("DEBUG: Parsed date '%s' using layout '%s'", trimmedDateStr, layout)
			break // Stop on first successful parse
		}
	}

	if parseSuccess {
		// Successfully parsed, now format as YYYY-MM-DD in UTC
		utcFormattedDate := parsedTime.UTC().Format("2006-01-02")
		log.Printf("DEBUG: Original parsed time: %s, UTC formatted date: %s", parsedTime.String(), utcFormattedDate)
		return utcFormattedDate, true
	}

	// If parsing failed with all known layouts
	log.Printf("WARN: Failed to parse date string '%s' with known layouts.", trimmedDateStr)
	// Return the original string but indicate failure
	return trimmedDateStr, false
}

/* Keeping this schema here on the off chance that they fix this for 2.0-flash
model.ResponseMIMEType = "application/json"
responseSchema := &genai.Schema{
	Type: genai.TypeObject,
	Properties: map[string]*genai.Schema{
		"title":         {Type: genai.TypeString},
		"author":        {Type: genai.TypeString},
		"summary":       {Type: genai.TypeString},
		"datePublished": {Type: genai.TypeString},
		"type":          {Type: genai.TypeString},
	},
	Required: []string{"title", "author", "summary", "type"},
}
var dynamicThreshold float32 = 0.6

config := &genai.GenerateContentConfig{
	// Response Schema isn't supported with GenerateContentStream, but GenerateContent doesn't support Search...
	ResponseMIMEType: "application/json",
	ResponseSchema:   responseSchema,
	Tools: []*genai.Tool{
		{
			// For some reason Retrieval is not supported, yet it's in the interface...
			GoogleSearchRetrieval: &genai.GoogleSearchRetrieval{
				DynamicRetrievalConfig: &genai.DynamicRetrievalConfig{
					DynamicThreshold: &dynamicThreshold,
				},
			},
		},
	},
}
*/
