package traffic

import (
	"fmt"
	"time"

	"github.com/OnnaSoft/lipstick/server/config"
	"github.com/OnnaSoft/lipstick/server/db"
	"gorm.io/gorm"
)

// updateDatabase adds bytes to today's counter of a domain. It returns an error
// instead of stopping the process, so the caller can keep the bytes for later.
func (tm *TrafficManager) updateDatabase(domain string, traffic int64) error {
	conf, err := config.GetConfig()
	if err != nil {
		return fmt.Errorf("cannot read configuration: %w", err)
	}

	connection, err := db.GetConnection(conf.Database)
	if err != nil {
		return fmt.Errorf("cannot connect to database: %w", err)
	}

	today := time.Now().Truncate(24 * time.Hour)
	month := today.Format("2006-01")

	var dailyConsumption db.DailyConsumption
	err = connection.Where("domain = ? AND date = ?", domain, today).First(&dailyConsumption).Error
	if err != nil {
		if err != gorm.ErrRecordNotFound {
			return fmt.Errorf("cannot query daily consumption: %w", err)
		}
		newConsumption := db.DailyConsumption{
			Domain:    domain,
			Date:      today,
			Month:     month,
			BytesUsed: traffic,
		}
		if err := connection.Create(&newConsumption).Error; err != nil {
			return fmt.Errorf("cannot create daily consumption: %w", err)
		}
		return nil
	}

	if err := connection.Model(&dailyConsumption).Update("bytes_used", gorm.Expr("bytes_used + ?", traffic)).Error; err != nil {
		return fmt.Errorf("cannot update daily consumption: %w", err)
	}
	return nil
}
