package cmd

import (
	"encoding/json"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/finnhambly/antistatic-cli/internal/output"
	"github.com/spf13/cobra"
)

type commentItem struct {
	ID         int           `json:"id"`
	BodyText   string        `json:"body_text"`
	ParentID   *int          `json:"parent_id"`
	CreatedVia string        `json:"created_via"`
	Score      int           `json:"score"`
	TotalTips  int           `json:"total_tips"`
	ReplyCount int           `json:"reply_count"`
	InsertedAt string        `json:"inserted_at"`
	User       *commentUser  `json:"user"`
	Children   []commentItem `json:"children"`
}

type commentUser struct {
	ID       int    `json:"id"`
	Username string `json:"username"`
}

type commentsPayload struct {
	Comments      []commentItem `json:"comments"`
	ReturnedCount int           `json:"returned_count"`
	TotalCount    int           `json:"total_count"`
	Truncated     bool          `json:"truncated"`
	Sort          string        `json:"sort"`
	Filter        string        `json:"filter"`
	Paging        struct {
		Limit      int  `json:"limit"`
		HasMore    bool `json:"has_more"`
		NextCursor *struct {
			InsertedAt string `json:"inserted_at"`
			ID         int    `json:"id"`
		} `json:"next_cursor"`
	} `json:"paging"`
}

var commentsCmd = &cobra.Command{
	Use:   "comments <code>",
	Short: "List comments for a market",
	Long: `Retrieve comments with pagination and context controls.

Use --sort and --filter to change ordering/scoping.
Use --limit/--max-comments/--max-body-chars to keep output compact.
Use --cursor-inserted-at and --cursor-id to fetch the next page.`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := requireAuth(); err != nil {
			return err
		}

		code := args[0]
		sortBy, _ := cmd.Flags().GetString("sort")
		filterBy, _ := cmd.Flags().GetString("filter")
		limit, _ := cmd.Flags().GetInt("limit")
		maxComments, _ := cmd.Flags().GetInt("max-comments")
		maxBodyChars, _ := cmd.Flags().GetInt("max-body-chars")
		replies, _ := cmd.Flags().GetBool("replies")
		cursorInsertedAt, _ := cmd.Flags().GetString("cursor-inserted-at")
		cursorID, _ := cmd.Flags().GetInt("cursor-id")
		followingIDs, _ := cmd.Flags().GetString("following-ids")

		params := url.Values{}
		if sortBy != "" {
			params.Set("sort", sortBy)
		}
		if filterBy != "" {
			params.Set("filter", filterBy)
		}
		if limit > 0 {
			params.Set("limit", strconv.Itoa(limit))
		}
		if maxComments > 0 {
			params.Set("max_comments", strconv.Itoa(maxComments))
		}
		if maxBodyChars >= 0 {
			params.Set("max_body_chars", strconv.Itoa(maxBodyChars))
		}
		params.Set("replies", strconv.FormatBool(replies))
		if cursorInsertedAt != "" {
			params.Set("cursor_inserted_at", cursorInsertedAt)
		}
		if cursorID > 0 {
			params.Set("cursor_id", strconv.Itoa(cursorID))
		}
		if followingIDs != "" {
			params.Set("following_ids", followingIDs)
		}

		resp, err := client.Get("/markets/"+code+"/comments", params)
		if err != nil {
			return err
		}

		data, err := resp.Data()
		if err != nil {
			return err
		}

		if jsonOutput || !output.IsTTY() {
			output.JSON(data)
			return nil
		}

		var payload commentsPayload
		if err := json.Unmarshal(data, &payload); err != nil {
			output.JSON(data)
			return nil
		}

		if len(payload.Comments) == 0 {
			fmt.Println("No comments in this page.")
			return nil
		}

		headers := []string{"ID", "WHEN", "USER", "SCORE", "TIPS", "TEXT"}
		rows := make([][]string, 0, payload.ReturnedCount)
		flattenCommentRows(payload.Comments, 0, &rows)
		output.Table(headers, rows)

		fmt.Printf(
			"Showing %d of %d comments (sort=%s, filter=%s).\n",
			payload.ReturnedCount,
			payload.TotalCount,
			payload.Sort,
			payload.Filter,
		)
		if payload.Truncated {
			fmt.Println("Results were truncated by max-comments.")
		}
		if payload.Paging.HasMore && payload.Paging.NextCursor != nil {
			fmt.Printf(
				"Next page: antistatic comments %s --cursor-inserted-at %s --cursor-id %d\n",
				code,
				payload.Paging.NextCursor.InsertedAt,
				payload.Paging.NextCursor.ID,
			)
		}
		return nil
	},
}

func flattenCommentRows(comments []commentItem, depth int, rows *[][]string) {
	prefix := strings.Repeat("  ", depth)

	for _, comment := range comments {
		username := "-"
		if comment.User != nil && comment.User.Username != "" {
			username = comment.User.Username
		}

		text := strings.TrimSpace(comment.BodyText)
		if text == "" {
			text = "[no text]"
		}
		if len(text) > 140 {
			text = text[:137] + "..."
		}
		if comment.ReplyCount > 0 && len(comment.Children) == 0 {
			text += fmt.Sprintf(" [replies: %d]", comment.ReplyCount)
		}

		when := comment.InsertedAt
		if parsed, err := time.Parse(time.RFC3339, comment.InsertedAt); err == nil {
			when = parsed.UTC().Format("2006-01-02 15:04")
		}

		*rows = append(*rows, []string{
			fmt.Sprintf("%d", comment.ID),
			when,
			username,
			fmt.Sprintf("%d", comment.Score),
			fmt.Sprintf("%d", comment.TotalTips),
			prefix + text,
		})

		if len(comment.Children) > 0 {
			flattenCommentRows(comment.Children, depth+1, rows)
		}
	}
}

func init() {
	commentsCmd.Flags().String("sort", "newest", "Sort order: newest, oldest, hot, most_upvoted, most_tipped")
	commentsCmd.Flags().String("filter", "all", "Filter: all, bot, following")
	commentsCmd.Flags().IntP("limit", "l", 10, "Maximum root comments per page")
	commentsCmd.Flags().Int("max-comments", 80, "Maximum total comments returned (including replies)")
	commentsCmd.Flags().Int("max-body-chars", 500, "Maximum body chars per comment (0 disables truncation)")
	commentsCmd.Flags().Bool("replies", true, "Include replies")
	commentsCmd.Flags().String("cursor-inserted-at", "", "Pagination cursor timestamp (ISO8601)")
	commentsCmd.Flags().Int("cursor-id", 0, "Pagination cursor id")
	commentsCmd.Flags().String("following-ids", "", "Comma-separated user ids when --filter=following")

	rootCmd.AddCommand(commentsCmd)
}
