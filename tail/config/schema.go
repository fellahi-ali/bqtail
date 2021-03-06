package config

import "google.golang.org/api/bigquery/v2"

//Schema represents schema
type Schema struct {
	Template          string
	TransientTemplate string
	templateReference *bigquery.TableReference
	Table             *bigquery.TableSchema
	Autodetect        bool
	Split             *Split
}
