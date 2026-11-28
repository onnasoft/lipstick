package manager

import (
	"sort"
	"strings"
	"time"

	"github.com/OnnaSoft/lipstick/logger"
	"github.com/OnnaSoft/lipstick/server/db"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// The state of the server is written to the database, so every admin API in a cluster
// answers with the same numbers. Each server publishes its own rows; a reader adds up
// the rows of the servers that reported recently.

const (
	// NodeStatusEvery is how often a server publishes its state.
	NodeStatusEvery = 5 * time.Second
	// NodeStatusFreshness is how old a report may be and still count. A server that
	// stops reporting disappears from the totals after this.
	NodeStatusFreshness = 20 * time.Second
	// nodeStatusKeep is how long rows of silent servers stay before they are deleted.
	nodeStatusKeep = 5 * time.Minute
)

// PublishNodeStatus writes the state of one server: a row for each domain it serves,
// and removal of rows for domains it no longer serves. Rows older than nodeStatusKeep
// are deleted for every server.
func PublishNodeStatus(conn *gorm.DB, nodeID string, stats []DomainStats, now time.Time) error {
	return conn.Transaction(func(tx *gorm.DB) error {
		names := make([]string, 0, len(stats))
		for _, s := range stats {
			names = append(names, s.Domain)
			row := db.NodeStatus{
				NodeID:          nodeID,
				Domain:          s.Domain,
				Status:          s.Status,
				Clients:         s.Clients,
				ActiveSessions:  s.ActiveSessions,
				PendingVisitors: s.PendingVisitors,
				BytesSinceStart: s.BytesSinceStart,
				IntranetPorts:   strings.Join(s.IntranetPorts, ","),
				UpdatedAt:       now,
			}
			if err := tx.Clauses(clause.OnConflict{
				Columns:   []clause.Column{{Name: "node_id"}, {Name: "domain"}},
				UpdateAll: true,
			}).Create(&row).Error; err != nil {
				return err
			}
		}
		del := tx.Where("node_id = ?", nodeID)
		if len(names) > 0 {
			del = del.Where("domain NOT IN ?", names)
		}
		if err := del.Delete(&db.NodeStatus{}).Error; err != nil {
			return err
		}
		return tx.Where("updated_at < ?", now.Add(-nodeStatusKeep)).Delete(&db.NodeStatus{}).Error
	})
}

// statusRank orders states so that a domain served by several servers shows the best one.
func statusRank(status string) int {
	switch status {
	case "active":
		return 3
	case "idle":
		return 2
	case "unresponsive":
		return 1
	}
	return 0
}

// ReadClusterStatus returns the state of every domain, added up over the servers that
// reported within NodeStatusFreshness. The result is sorted by domain name.
func ReadClusterStatus(conn *gorm.DB, now time.Time) ([]DomainStats, error) {
	var rows []db.NodeStatus
	if err := conn.Where("updated_at >= ?", now.Add(-NodeStatusFreshness)).Find(&rows).Error; err != nil {
		return nil, err
	}
	byDomain := map[string]*DomainStats{}
	ports := map[string]map[string]bool{}
	for _, r := range rows {
		agg, ok := byDomain[r.Domain]
		if !ok {
			agg = &DomainStats{Domain: r.Domain, Status: r.Status}
			byDomain[r.Domain] = agg
			ports[r.Domain] = map[string]bool{}
		}
		if statusRank(r.Status) > statusRank(agg.Status) {
			agg.Status = r.Status
		}
		agg.Clients += r.Clients
		agg.ActiveSessions += r.ActiveSessions
		agg.PendingVisitors += r.PendingVisitors
		agg.BytesSinceStart += r.BytesSinceStart
		for _, p := range strings.Split(r.IntranetPorts, ",") {
			if p != "" {
				ports[r.Domain][p] = true
			}
		}
	}
	out := make([]DomainStats, 0, len(byDomain))
	for name, agg := range byDomain {
		for p := range ports[name] {
			agg.IntranetPorts = append(agg.IntranetPorts, p)
		}
		sort.Strings(agg.IntranetPorts)
		out = append(out, *agg)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Domain < out[j].Domain })
	return out, nil
}

// StartNodeStatus publishes this server's state to the database every NodeStatusEvery,
// until Close. Close removes this server's rows, so the others stop counting it at once.
func (m *Manager) StartNodeStatus(conn *gorm.DB, nodeID string) {
	m.nodeStop = make(chan struct{})
	m.nodeDone = make(chan struct{})
	go func() {
		defer close(m.nodeDone)
		ticker := time.NewTicker(NodeStatusEvery)
		defer ticker.Stop()
		failing := false
		for {
			select {
			case <-m.nodeStop:
				if err := PublishNodeStatus(conn, nodeID, nil, time.Now()); err != nil {
					logger.Default.Error("node status: could not remove this server's rows:", err)
				}
				return
			case now := <-ticker.C:
				err := PublishNodeStatus(conn, nodeID, m.Snapshot(), now)
				if err != nil && !failing {
					logger.Default.Error("node status: the database does not accept this server's state:", err)
				}
				if err == nil && failing {
					logger.Default.Info("node status: reporting again")
				}
				failing = err != nil
			}
		}
	}()
}

// stopNodeStatus ends the publisher, if one was started, and waits for its last write.
func (m *Manager) stopNodeStatus() {
	if m.nodeStop == nil {
		return
	}
	close(m.nodeStop)
	<-m.nodeDone
	m.nodeStop = nil
}
