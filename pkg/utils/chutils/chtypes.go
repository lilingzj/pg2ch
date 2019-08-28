package chutils

import (
	"fmt"
	"strings"

	"github.com/jackc/pgx/pgtype"

	"github.com/mkabilov/pg2ch/pkg/config"
	"github.com/mkabilov/pg2ch/pkg/message"
	"github.com/mkabilov/pg2ch/pkg/utils"
	"github.com/mkabilov/pg2ch/pkg/utils/dbtypes"
	"github.com/mkabilov/pg2ch/pkg/utils/pgutils"
)

const (
	pgTrue  = 't'
	pgFalse = 'f'

	//ajBool specific value
	ajBoolUnknown = 'u'
	null          = "null"

	timestampLength = 19 // ->2019-06-08 15:50:01<- ClickHouse does not support milliseconds
)

var (
	escaper = strings.NewReplacer(`\`, `\\`, `'`, `\'`)

	pgToChMap = map[string]string{
		dbtypes.PgSmallint:                 dbtypes.ChInt16,
		dbtypes.PgInteger:                  dbtypes.ChInt32,
		dbtypes.PgBigint:                   dbtypes.ChInt64,
		dbtypes.PgCharacterVarying:         dbtypes.ChString,
		dbtypes.PgVarchar:                  dbtypes.ChString,
		dbtypes.PgText:                     dbtypes.ChString,
		dbtypes.PgReal:                     dbtypes.ChFloat32,
		dbtypes.PgDoublePrecision:          dbtypes.ChFloat64,
		dbtypes.PgInterval:                 dbtypes.ChInt32,
		dbtypes.PgBoolean:                  dbtypes.ChUInt8,
		dbtypes.PgDecimal:                  dbtypes.ChDecimal,
		dbtypes.PgNumeric:                  dbtypes.ChDecimal,
		dbtypes.PgCharacter:                dbtypes.ChFixedString,
		dbtypes.PgChar:                     dbtypes.ChFixedString,
		dbtypes.PgJsonb:                    dbtypes.ChString,
		dbtypes.PgJson:                     dbtypes.ChString,
		dbtypes.PgUuid:                     dbtypes.ChUUID,
		dbtypes.PgBytea:                    dbtypes.ChUInt8Array,
		dbtypes.PgInet:                     dbtypes.ChInt64,
		dbtypes.PgTimestamp:                dbtypes.ChDateTime,
		dbtypes.PgTimestampWithTimeZone:    dbtypes.ChDateTime,
		dbtypes.PgTimestampWithoutTimeZone: dbtypes.ChDateTime,
		dbtypes.PgDate:                     dbtypes.ChDate,
		dbtypes.PgTime:                     dbtypes.ChUint32,
		dbtypes.PgTimeWithoutTimeZone:      dbtypes.ChUint32,
		dbtypes.PgTimeWithTimeZone:         dbtypes.ChUint32,

		// adjust gmbh specific types
		dbtypes.PgAdjustIstore:    dbtypes.ChInt32,    // type for istore values
		dbtypes.PgAdjustBigIstore: dbtypes.ChInt64,    // type for bigistore values
		dbtypes.PgAdjustAjTime:    dbtypes.ChDateTime, // adjust time
		dbtypes.PgAdjustAjDate:    dbtypes.ChDate,     // adjust date
		dbtypes.PgAdjustAjBool:    dbtypes.ChUInt8,    // adjust boolean: true, false, unknown
	}

	ajBoolUnknownValue = []byte("-1")
	nullStr            = []byte(`\N`)
	istoreNull         = []byte("[]\t[]")
)

// ToClickHouseType converts pg type into ClickHouse type
func ToClickHouseType(pgColumn config.PgColumn) (string, error) {
	chType, ok := pgToChMap[pgColumn.BaseType]
	if !ok {
		chType = dbtypes.ChString
	}

	switch pgColumn.BaseType {
	case dbtypes.PgDecimal:
		fallthrough
	case dbtypes.PgNumeric:
		if pgColumn.Ext == nil {
			return "", fmt.Errorf("precision must be specified for the numeric type")
		}
		chType = fmt.Sprintf("%s(%d, %d)", chType, pgColumn.Ext[0], pgColumn.Ext[1])
	case dbtypes.PgCharacter:
		fallthrough
	case dbtypes.PgChar:
		if pgColumn.Ext == nil {
			return "", fmt.Errorf("length must be specified for character type")
		}
		chType = fmt.Sprintf("%s(%d)", chType, pgColumn.Ext[0])
	case dbtypes.PgAdjustCountry:
		fallthrough
	case dbtypes.PgAdjustOsName:
		fallthrough
	case dbtypes.PgAdjustDeviceType:
		chType = fmt.Sprintf("LowCardinality(%s)", chType)
	}

	if pgColumn.IsArray {
		chType = fmt.Sprintf("Array(%s)", chType)
	}

	if pgColumn.IsNullable && !pgColumn.IsArray {
		chType = fmt.Sprintf("Nullable(%s)", chType)
	}

	return chType, nil
}

func convertBaseType(buf utils.Writer, baseType string, tupleData *message.Tuple, colProp config.ColumnProperty) error {
	var w []byte

	if tupleData.Kind == message.TupleNull {
		if len(colProp.Coalesce) > 0 {
			w = colProp.Coalesce
		} else {
			w = nullStr
		}

		return nil
	} else {
		w = tupleData.Value
	}

	switch baseType {
	case dbtypes.PgAdjustIstore:
		fallthrough
	case dbtypes.PgAdjustBigIstore:
		if tupleData.Kind == message.TupleNull {
			w = istoreNull
		} else {
			return pgutils.IstoreToArrays(buf, tupleData.Value)
		}
	case dbtypes.PgAdjustAjBool:
		fallthrough
	case dbtypes.PgBoolean:
		if tupleData.Kind != message.TupleNull {
			switch tupleData.Value[0] {
			case pgTrue:
				return buf.WriteByte('1')
			case pgFalse:
				return buf.WriteByte('0')
			case ajBoolUnknown:
				w = ajBoolUnknownValue
			}
		}
	case dbtypes.PgTimestampWithTimeZone:
		fallthrough
	case dbtypes.PgTimestampWithoutTimeZone:
		fallthrough
	case dbtypes.PgTimestamp:
		if tupleData.Kind != message.TupleNull {
			_, err := buf.Write(tupleData.Value[:timestampLength])
			return err
		}
	case dbtypes.PgTime:
		fallthrough
	case dbtypes.PgTimeWithoutTimeZone:
		//TODO
	case dbtypes.PgTimeWithTimeZone:
		//TODO
	}

	_, err := buf.Write(w)
	return err
}

func ConvertColumn(w utils.Writer, column config.PgColumn, tupleData *message.Tuple, colProp config.ColumnProperty) error {
	if !column.IsArray {
		return convertBaseType(w, column.BaseType, tupleData, colProp)
	}

	err := w.WriteByte('[')
	if err != nil {
		return err
	}

	switch column.BaseType {
	case dbtypes.PgBigint:
		fallthrough
	case dbtypes.PgInteger:
		_, err = w.Write(tupleData.Value[1 : len(tupleData.Value)-1])
	default:
		val := pgtype.TextArray{}
		if err := val.DecodeText(nil, tupleData.Value); err != nil {
			return err
		}

		first := true
		for _, val := range val.Elements {
			if !first {
				if err := w.WriteByte(','); err != nil {
					return err
				}
			}

			first = false

			if val.Status == pgtype.Null {
				if _, err := w.Write([]byte(null)); err != nil {
					return err
				}
			} else {
				if _, err := w.Write([]byte("'" + escaper.Replace(val.String) + "'")); err != nil {
					return err
				}
			}
		}
	}

	return w.WriteByte(']')
}
