### Data aggregation with side input with yaml rule

### Scenario:

This scenario test data aggregation with side input.

It uses the following rule:

[@rule.json](rule/rule.yaml)
```yaml
- When:
    Prefix: "/data/case022"
    Suffix: ".json"
  Async: true
  Batch:
    Window:
      DurationInSec: 10
  Dest:
    Table: bqtail.transactions
    TransientDataset: temp
    TransientAlias: t
    Transform:
      charge: (CASE WHEN type_id = 1 THEN t.payment + f.value WHEN type_id = 2 THEN t.payment * (1 + f.value) END)
    SideInputs:
      - Table: bqtail.fees
        Alias: f
        'On': t.fee_id = f.id
  OnSuccess:
    - Action: query
      Request:
        SQL: SELECT
          DATE(timestamp) AS date,
          sku_id,
          supply_entity_id,
          MAX($EventID) AS batch_id,
          SUM( payment) payment,
          SUM((CASE WHEN type_id = 1 THEN t.payment + f.value WHEN type_id = 2 THEN t.payment * (1 + f.value) END)) charge,
          SUM(COALESCE(qty, 1.0)) AS qty
          FROM $TempTable t
          LEFT JOIN bqtail.fees f ON f.id = t.fee_id
          GROUP BY 1, 2, 3
        Dest: bqtail.supply_performance
        Append: true
      OnSuccess:
        - Action: delete
```


Note that storage trigger $EventID is used as batch id.

