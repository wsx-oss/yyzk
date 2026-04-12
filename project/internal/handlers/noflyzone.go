package handlers

import (
	"database/sql"
	"os"
	"strings"

	"github.com/gin-gonic/gin"
)

// ==================== No-Fly Zone API ====================

func (a *API) NoFlyZoneList(c *gin.Context) {
	rows, err := a.db.Query(`SELECT id, name, zone_type, shape_type, shape_json, altitude_limit, altitude_enabled, area_m2, address, created_at FROM no_fly_zones ORDER BY datetime(created_at) DESC`)
	if err != nil {
		c.JSON(500, gin.H{"error": err.Error()})
		return
	}
	defer rows.Close()
	items := []gin.H{}
	for rows.Next() {
		var id, altLimit, altEnabled int
		var name, zoneType, shapeType, shapeJSON, address string
		var areaM2 float64
		var createdAt sql.NullString
		if err := rows.Scan(&id, &name, &zoneType, &shapeType, &shapeJSON, &altLimit, &altEnabled, &areaM2, &address, &createdAt); err == nil {
			items = append(items, gin.H{
				"id": id, "name": name, "zone_type": zoneType, "shape_type": shapeType,
				"shape_json": shapeJSON, "altitude_limit": altLimit, "altitude_enabled": altEnabled == 1,
				"area_m2": areaM2, "address": address, "created_at": createdAt.String,
			})
		}
	}
	c.JSON(200, gin.H{"items": items, "total": len(items)})
}

func (a *API) NoFlyZoneCreate(c *gin.Context) {
	var p struct {
		Name            string  `json:"name"`
		ZoneType        string  `json:"zone_type"`
		ShapeType       string  `json:"shape_type"`
		ShapeJSON       string  `json:"shape_json"`
		AltitudeLimit   int     `json:"altitude_limit"`
		AltitudeEnabled bool    `json:"altitude_enabled"`
		AreaM2          float64 `json:"area_m2"`
		Address         string  `json:"address"`
	}
	if err := c.BindJSON(&p); err != nil {
		c.JSON(400, gin.H{"error": "bad json"})
		return
	}
	p.Name = strings.TrimSpace(p.Name)
	if p.Name == "" {
		c.JSON(400, gin.H{"error": "禁飞区名称不能为空"})
		return
	}
	if p.ZoneType == "" {
		p.ZoneType = "禁飞区"
	}
	if p.ShapeType == "" {
		p.ShapeType = "polygon"
	}
	if p.ShapeJSON == "" {
		p.ShapeJSON = "[]"
	}
	if !p.AltitudeEnabled {
		p.AltitudeLimit = -1
	}
	var cnt int
	a.db.QueryRow(`SELECT COUNT(*) FROM no_fly_zones WHERE name=?`, p.Name).Scan(&cnt)
	if cnt > 0 {
		c.JSON(400, gin.H{"error": "禁飞区名称已存在，请使用不同的名称"})
		return
	}
	altEnabled := 0
	if p.AltitudeEnabled {
		altEnabled = 1
	}
	res, err := a.db.Exec(
		`INSERT INTO no_fly_zones(name, zone_type, shape_type, shape_json, altitude_limit, altitude_enabled, area_m2, address) VALUES(?,?,?,?,?,?,?,?)`,
		p.Name, p.ZoneType, p.ShapeType, p.ShapeJSON, p.AltitudeLimit, altEnabled, p.AreaM2, p.Address,
	)
	if err != nil {
		c.JSON(500, gin.H{"error": err.Error()})
		return
	}
	id, _ := res.LastInsertId()
	c.JSON(200, gin.H{"ok": true, "id": id})
}

func (a *API) NoFlyZoneGet(c *gin.Context) {
	id := c.Param("id")
	var zoneID, altLimit, altEnabled int
	var name, zoneType, shapeType, shapeJSON, address string
	var areaM2 float64
	var createdAt sql.NullString
	err := a.db.QueryRow(
		`SELECT id, name, zone_type, shape_type, shape_json, altitude_limit, altitude_enabled, area_m2, address, created_at FROM no_fly_zones WHERE id=?`, id,
	).Scan(&zoneID, &name, &zoneType, &shapeType, &shapeJSON, &altLimit, &altEnabled, &areaM2, &address, &createdAt)
	if err != nil {
		c.JSON(404, gin.H{"error": "禁飞区不存在"})
		return
	}
	c.JSON(200, gin.H{
		"id": zoneID, "name": name, "zone_type": zoneType, "shape_type": shapeType,
		"shape_json": shapeJSON, "altitude_limit": altLimit, "altitude_enabled": altEnabled == 1,
		"area_m2": areaM2, "address": address, "created_at": createdAt.String,
	})
}

func (a *API) NoFlyZoneUpdate(c *gin.Context) {
	id := c.Param("id")
	var p struct {
		Name            string  `json:"name"`
		ZoneType        string  `json:"zone_type"`
		ShapeType       string  `json:"shape_type"`
		ShapeJSON       string  `json:"shape_json"`
		AltitudeLimit   int     `json:"altitude_limit"`
		AltitudeEnabled bool    `json:"altitude_enabled"`
		AreaM2          float64 `json:"area_m2"`
		Address         string  `json:"address"`
	}
	if err := c.BindJSON(&p); err != nil {
		c.JSON(400, gin.H{"error": "bad json"})
		return
	}
	p.Name = strings.TrimSpace(p.Name)
	if p.Name == "" {
		c.JSON(400, gin.H{"error": "名称不能为空"})
		return
	}
	var cnt int
	a.db.QueryRow(`SELECT COUNT(*) FROM no_fly_zones WHERE name=? AND id!=?`, p.Name, id).Scan(&cnt)
	if cnt > 0 {
		c.JSON(400, gin.H{"error": "禁飞区名称已存在"})
		return
	}
	altEnabled := 0
	if p.AltitudeEnabled {
		altEnabled = 1
	}
	if !p.AltitudeEnabled {
		p.AltitudeLimit = -1
	}
	result, err := a.db.Exec(
		`UPDATE no_fly_zones SET name=?, zone_type=?, shape_type=?, shape_json=?, altitude_limit=?, altitude_enabled=?, area_m2=?, address=?, updated_at=CURRENT_TIMESTAMP WHERE id=?`,
		p.Name, p.ZoneType, p.ShapeType, p.ShapeJSON, p.AltitudeLimit, altEnabled, p.AreaM2, p.Address, id,
	)
	if err != nil {
		c.JSON(500, gin.H{"error": err.Error()})
		return
	}
	aff, _ := result.RowsAffected()
	if aff == 0 {
		c.JSON(404, gin.H{"error": "禁飞区不存在"})
		return
	}
	c.JSON(200, gin.H{"ok": true})
}

func (a *API) NoFlyZoneDelete(c *gin.Context) {
	id := c.Param("id")
	result, err := a.db.Exec(`DELETE FROM no_fly_zones WHERE id=?`, id)
	if err != nil {
		c.JSON(500, gin.H{"error": err.Error()})
		return
	}
	aff, _ := result.RowsAffected()
	if aff == 0 {
		c.JSON(404, gin.H{"error": "禁飞区不存在"})
		return
	}
	c.JSON(200, gin.H{"ok": true})
}

// AppConfig returns public frontend configuration (non-secret keys for map tiles etc.)
func (a *API) AppConfig(c *gin.Context) {
	key := os.Getenv("TIANDITU_KEY")
	if key == "" {
		key = "1d109683f4d84198e37a38c442d68311"
	}
	enableRegister := true
	if v := os.Getenv("ENABLE_REGISTER"); v == "false" || v == "0" {
		enableRegister = false
	}
	c.JSON(200, gin.H{"tianditu_key": key, "enable_register": enableRegister})
}
