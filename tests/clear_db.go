package tests

import (
	"ig_server/config"
)

func ClearDB() {
	db := config.GetDB()

	tables, err := db.Migrator().GetTables()

	if err != nil {
		panic(err)
	}

	for _, table := range tables {
		if table != "migrations" {
			db.Exec("DELETE FROM " + table)
		}
	}
}
