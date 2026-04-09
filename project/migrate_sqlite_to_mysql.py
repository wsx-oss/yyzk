"""
Migrate all data from local SQLite (app.db) to remote MySQL (Aiven).
Run: python migrate_sqlite_to_mysql.py
"""
import sqlite3
import pymysql
import ssl
import sys

# ── Configuration ──────────────────────────────────────────────────────────────
SQLITE_PATH = r"d:\code\Intelligent-Tool-Human-Computer-Interaction-and-Remote-Control-System\project\app.db"
MYSQL_HOST = "cloudcontrol-tolovelife-49cf.i.aivencloud.com"
MYSQL_PORT = 16898
MYSQL_USER = "avnadmin"
MYSQL_PASS = "AVNS_cKxuMjfim1GfuDoshqq"
MYSQL_DB   = "defaultdb"

# Tables to migrate in order (respects foreign-key dependencies).
# Tables with FK references come after their parent tables.
ORDERED_TABLES = [
    "users",
    "sessions",
    "user_stats",
    "recordings",
    "logs",
    "alerts",
    "updates",
    "sync_status",
    "devices",
    "video_sources",
    "hardware_items",
    "sync_tasks",
    "perf_reports",
    "flight_missions",
    "mission_logs",
    "gps_devices",
    "gps_history",
    "gps_fence_alerts",
    "drones",
    "battery_records",
    "battery_alerts",
    "flight_plans",
    "no_fly_zones",
    "cot_chains",
    "notifications",
    "ai_chat_messages",
]


def get_sqlite_columns(cursor, table):
    """Return list of column names for a SQLite table."""
    cursor.execute(f"PRAGMA table_info([{table}])")
    return [row[1] for row in cursor.fetchall()]


def get_mysql_columns(cursor, table):
    """Return list of column names for a MySQL table."""
    cursor.execute(f"SHOW COLUMNS FROM `{table}`")
    return [row[0] for row in cursor.fetchall()]


def migrate_table(sqlite_cur, mysql_conn, mysql_cur, table):
    """Migrate one table from SQLite to MySQL."""
    # Get column info from both sides
    sqlite_cols = get_sqlite_columns(sqlite_cur, table)
    try:
        mysql_cols = get_mysql_columns(mysql_cur, table)
    except Exception as e:
        print(f"  ⚠ Table `{table}` does not exist in MySQL, skipping: {e}")
        return 0

    # Only migrate columns that exist in BOTH databases
    common_cols = [c for c in sqlite_cols if c in mysql_cols]
    if not common_cols:
        print(f"  ⚠ No common columns for `{table}`, skipping")
        return 0

    # Read all rows from SQLite
    col_list = ", ".join(f"[{c}]" for c in common_cols)
    sqlite_cur.execute(f"SELECT {col_list} FROM [{table}]")
    rows = sqlite_cur.fetchall()
    if not rows:
        print(f"  `{table}`: 0 rows (empty)")
        return 0

    # Build INSERT IGNORE statement for MySQL
    mysql_col_list = ", ".join(f"`{c}`" for c in common_cols)
    placeholders = ", ".join(["%s"] * len(common_cols))

    # For tables with non-auto-increment PK (cot_chains, sync_status, user_stats), use REPLACE
    # For others, use INSERT IGNORE to skip duplicates
    if table in ("cot_chains", "sync_status", "user_stats"):
        sql = f"REPLACE INTO `{table}` ({mysql_col_list}) VALUES ({placeholders})"
    else:
        sql = f"INSERT IGNORE INTO `{table}` ({mysql_col_list}) VALUES ({placeholders})"

    # Insert in batches
    batch_size = 100
    inserted = 0
    for i in range(0, len(rows), batch_size):
        batch = rows[i:i+batch_size]
        # Convert None values and handle encoding
        cleaned = []
        for row in batch:
            cleaned_row = []
            for val in row:
                if isinstance(val, bytes):
                    try:
                        val = val.decode("utf-8")
                    except Exception:
                        val = val.hex()
                cleaned_row.append(val)
            cleaned.append(tuple(cleaned_row))
        try:
            mysql_cur.executemany(sql, cleaned)
            inserted += mysql_cur.rowcount
        except Exception as e:
            print(f"  ✗ Error inserting batch into `{table}`: {e}")
            # Try one-by-one for this batch to skip bad rows
            for row in cleaned:
                try:
                    mysql_cur.execute(sql, row)
                    inserted += mysql_cur.rowcount
                except Exception as e2:
                    print(f"    ✗ Skipping row: {e2}")
    
    mysql_conn.commit()
    print(f"  ✓ `{table}`: {inserted}/{len(rows)} rows migrated (common cols: {len(common_cols)}/{len(sqlite_cols)})")
    return inserted


def reset_auto_increment(mysql_cur, mysql_conn, table):
    """Reset AUTO_INCREMENT to max(id)+1 for tables with auto-increment PKs."""
    try:
        mysql_cur.execute(f"SELECT MAX(id) FROM `{table}`")
        max_id = mysql_cur.fetchone()[0]
        if max_id is not None:
            mysql_cur.execute(f"ALTER TABLE `{table}` AUTO_INCREMENT = {max_id + 1}")
            mysql_conn.commit()
    except Exception:
        pass  # Table may not have auto-increment id


def main():
    # Connect to SQLite
    print("Connecting to SQLite...")
    sqlite_conn = sqlite3.connect(SQLITE_PATH)
    sqlite_cur = sqlite_conn.cursor()

    # Connect to MySQL (with SSL)
    print("Connecting to MySQL...")
    mysql_conn = pymysql.connect(
        host=MYSQL_HOST,
        port=MYSQL_PORT,
        user=MYSQL_USER,
        password=MYSQL_PASS,
        database=MYSQL_DB,
        charset="utf8mb4",
        ssl={"ssl": True},
        ssl_verify_cert=False,
    )
    mysql_cur = mysql_conn.cursor()

    # Disable FK checks during migration
    mysql_cur.execute("SET FOREIGN_KEY_CHECKS = 0")
    mysql_conn.commit()

    print("\n=== Starting data migration ===\n")
    total = 0
    for table in ORDERED_TABLES:
        # Check if table exists in SQLite
        sqlite_cur.execute("SELECT name FROM sqlite_master WHERE type='table' AND name=?", (table,))
        if not sqlite_cur.fetchone():
            print(f"  ⚠ Table `{table}` not in SQLite, skipping")
            continue
        count = migrate_table(sqlite_cur, mysql_conn, mysql_cur, table)
        total += count

    # Reset auto-increment values
    print("\n=== Resetting AUTO_INCREMENT values ===\n")
    auto_inc_tables = [t for t in ORDERED_TABLES if t not in ("cot_chains", "sync_status", "user_stats")]
    for table in auto_inc_tables:
        reset_auto_increment(mysql_cur, mysql_conn, table)

    # Re-enable FK checks
    mysql_cur.execute("SET FOREIGN_KEY_CHECKS = 1")
    mysql_conn.commit()

    # Verify migration
    print("\n=== Verification: Row counts in MySQL ===\n")
    for table in ORDERED_TABLES:
        try:
            mysql_cur.execute(f"SELECT COUNT(*) FROM `{table}`")
            count = mysql_cur.fetchone()[0]
            sqlite_cur.execute(f"SELECT COUNT(*) FROM [{table}]")
            sqlite_count = sqlite_cur.fetchone()[0]
            status = "✓" if count >= sqlite_count else "✗ MISMATCH"
            print(f"  {status} `{table}`: MySQL={count}, SQLite={sqlite_count}")
        except Exception as e:
            print(f"  ✗ `{table}`: {e}")

    print(f"\n=== Migration complete: {total} total rows migrated ===")

    sqlite_conn.close()
    mysql_conn.close()


if __name__ == "__main__":
    main()
