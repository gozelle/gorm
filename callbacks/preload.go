package callbacks

import (
	"fmt"
	"reflect"
	
	"github.com/gozelle/gorm"
	"github.com/gozelle/gorm/clause"
	"github.com/gozelle/gorm/schema"
	"github.com/gozelle/gorm/utils"
)

func preload(tx *gorm.DB, rel *schema.Relationship, conds []interface{}, preloads map[string][]interface{}) error {
	var (
		reflectValue     = tx.Statement.ReflectValue
		relForeignKeys   []string
		relForeignFields []*schema.Field
		foreignFields    []*schema.Field
		foreignValues    [][]interface{}
		identityMap      = map[string][]reflect.Value{}
		inlineConds      []interface{}
	)
	
	if rel.JoinTable != nil {
		var (
			joinForeignFields    = make([]*schema.Field, 0, len(rel.References))
			joinRelForeignFields = make([]*schema.Field, 0, len(rel.References))
			joinForeignKeys      = make([]string, 0, len(rel.References))
		)
		
		for _, ref := range rel.References {
			if ref.OwnPrimaryKey {
				joinForeignKeys = append(joinForeignKeys, ref.ForeignKey.DBName)
				joinForeignFields = append(joinForeignFields, ref.ForeignKey)
				foreignFields = append(foreignFields, ref.PrimaryKey)
			} else if ref.PrimaryValue != "" {
				tx = tx.Where(clause.Eq{Column: ref.ForeignKey.DBName, Value: ref.PrimaryValue})
			} else {
				joinRelForeignFields = append(joinRelForeignFields, ref.ForeignKey)
				relForeignKeys = append(relForeignKeys, ref.PrimaryKey.DBName)
				relForeignFields = append(relForeignFields, ref.PrimaryKey)
			}
		}
		
		joinIdentityMap, joinForeignValues := schema.GetIdentityFieldValuesMap(tx.Statement.Context, reflectValue, foreignFields)
		if len(joinForeignValues) == 0 {
			return nil
		}
		
		joinResults := rel.JoinTable.MakeSlice().Elem()
		column, values := schema.ToQueryValues(clause.CurrentTable, joinForeignKeys, joinForeignValues)
		if err := tx.Where(clause.IN{Column: column, Values: values}).Find(joinResults.Addr().Interface()).Error; err != nil {
			return err
		}
		
		// convert join identity map to relation identity map
		fieldValues := make([]interface{}, len(joinForeignFields))
		joinFieldValues := make([]interface{}, len(joinRelForeignFields))
		for i := 0; i < joinResults.Len(); i++ {
			joinIndexValue := joinResults.Index(i)
			for idx, field := range joinForeignFields {
				fieldValues[idx], _ = field.ValueOf(tx.Statement.Context, joinIndexValue)
			}
			
			for idx, field := range joinRelForeignFields {
				joinFieldValues[idx], _ = field.ValueOf(tx.Statement.Context, joinIndexValue)
			}
			
			if results, ok := joinIdentityMap[utils.ToStringKey(fieldValues...)]; ok {
				joinKey := utils.ToStringKey(joinFieldValues...)
				identityMap[joinKey] = append(identityMap[joinKey], results...)
			}
		}
		
		_, foreignValues = schema.GetIdentityFieldValuesMap(tx.Statement.Context, joinResults, joinRelForeignFields)
	} else {
		for _, ref := range rel.References {
			if ref.OwnPrimaryKey {
				relForeignKeys = append(relForeignKeys, ref.ForeignKey.DBName)
				relForeignFields = append(relForeignFields, ref.ForeignKey)
				foreignFields = append(foreignFields, ref.PrimaryKey)
			} else if ref.PrimaryValue != "" {
				tx = tx.Where(clause.Eq{Column: ref.ForeignKey.DBName, Value: ref.PrimaryValue})
			} else {
				relForeignKeys = append(relForeignKeys, ref.PrimaryKey.DBName)
				relForeignFields = append(relForeignFields, ref.PrimaryKey)
				foreignFields = append(foreignFields, ref.ForeignKey)
			}
		}
		
		identityMap, foreignValues = schema.GetIdentityFieldValuesMap(tx.Statement.Context, reflectValue, foreignFields)
		if len(foreignValues) == 0 {
			return nil
		}
	}
	
	// nested preload
	for p, pvs := range preloads {
		tx = tx.Preload(p, pvs...)
	}
	
	reflectResults := rel.FieldSchema.MakeSlice().Elem()
	column, values := schema.ToQueryValues(clause.CurrentTable, relForeignKeys, foreignValues)
	
	if len(values) != 0 {
		for _, cond := range conds {
			if fc, ok := cond.(func(*gorm.DB) *gorm.DB); ok {
				tx = fc(tx)
			} else {
				inlineConds = append(inlineConds, cond)
			}
		}
		
		if err := tx.Where(clause.IN{Column: column, Values: values}).Find(reflectResults.Addr().Interface(), inlineConds...).Error; err != nil {
			return err
		}
	}
	
	fieldValues := make([]interface{}, len(relForeignFields))
	
	// clean up old values before preloading
	switch reflectValue.Kind() {
	case reflect.Struct:
		switch rel.Type {
		case schema.HasMany, schema.Many2Many:
			tx.AddError(rel.Field.Set(tx.Statement.Context, reflectValue, reflect.MakeSlice(rel.Field.IndirectFieldType, 0, 10).Interface()))
		default:
			tx.AddError(rel.Field.Set(tx.Statement.Context, reflectValue, reflect.New(rel.Field.FieldType).Interface()))
		}
	case reflect.Slice, reflect.Array:
		for i := 0; i < reflectValue.Len(); i++ {
			switch rel.Type {
			case schema.HasMany, schema.Many2Many:
				tx.AddError(rel.Field.Set(tx.Statement.Context, reflectValue.Index(i), reflect.MakeSlice(rel.Field.IndirectFieldType, 0, 10).Interface()))
			default:
				tx.AddError(rel.Field.Set(tx.Statement.Context, reflectValue.Index(i), reflect.New(rel.Field.FieldType).Interface()))
			}
		}
	}
	
	for i := 0; i < reflectResults.Len(); i++ {
		elem := reflectResults.Index(i)
		for idx, field := range relForeignFields {
			fieldValues[idx], _ = field.ValueOf(tx.Statement.Context, elem)
		}
		
		datas, ok := identityMap[utils.ToStringKey(fieldValues...)]
		if !ok {
			return fmt.Errorf("failed to assign association %#v, make sure foreign fields exists", elem.Interface())
		}
		
		for _, data := range datas {
			reflectFieldValue := rel.Field.ReflectValueOf(tx.Statement.Context, data)
			if reflectFieldValue.Kind() == reflect.Ptr && reflectFieldValue.IsNil() {
				reflectFieldValue.Set(reflect.New(rel.Field.FieldType.Elem()))
			}
			
			reflectFieldValue = reflect.Indirect(reflectFieldValue)
			switch reflectFieldValue.Kind() {
			case reflect.Struct:
				tx.AddError(rel.Field.Set(tx.Statement.Context, data, elem.Interface()))
			case reflect.Slice, reflect.Array:
				if reflectFieldValue.Type().Elem().Kind() == reflect.Ptr {
					tx.AddError(rel.Field.Set(tx.Statement.Context, data, reflect.Append(reflectFieldValue, elem).Interface()))
				} else {
					tx.AddError(rel.Field.Set(tx.Statement.Context, data, reflect.Append(reflectFieldValue, elem.Elem()).Interface()))
				}
			}
		}
	}

	return tx.Error
}
