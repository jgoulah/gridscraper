import hassapi as hass
import sqlite3
import json
from datetime import datetime

class BackfillState(hass.Hass):
    def initialize(self):
        # Register HTTP API endpoints
        self.register_endpoint(self.backfill_state, "backfill_state")
        self.register_endpoint(self.generate_statistics, "generate_statistics")
        self.register_endpoint(self.generate_cost_statistics, "generate_cost_statistics")
        self.log("Backfill State API endpoints registered:")
        self.log("  - /api/appdaemon/backfill_state")
        self.log("  - /api/appdaemon/generate_statistics")
        self.log("  - /api/appdaemon/generate_cost_statistics")

    def backfill_state(self, data, **kwargs):
        """Handle state backfill API calls - stores individual hourly consumption values"""
        entity_id = data.get("entity_id")
        state = data.get("state")
        last_changed = data.get("last_changed")
        last_updated = data.get("last_updated", last_changed)

        if not entity_id or not state or not last_changed:
            self.log("Missing required parameters", level="WARNING")
            return {"error": "Missing required parameters"}, 400

        try:
            conn = sqlite3.connect('/homeassistant/home-assistant_v2.db', timeout=30)
            cursor = conn.cursor()

            last_changed_dt = datetime.fromisoformat(last_changed.replace('Z', '+00:00'))
            last_updated_dt = datetime.fromisoformat(last_updated.replace('Z', '+00:00'))

            # Get or create metadata_id
            cursor.execute("""
                SELECT metadata_id FROM states_meta
                WHERE entity_id = ?
            """, (entity_id,))

            row = cursor.fetchone()
            if row:
                metadata_id = row[0]
            else:
                cursor.execute("""
                    INSERT INTO states_meta (entity_id)
                    VALUES (?)
                """, (entity_id,))
                metadata_id = cursor.lastrowid

            # Check for duplicates
            cursor.execute("""
                SELECT state_id FROM states
                WHERE metadata_id = ?
                AND last_changed_ts = ?
            """, (metadata_id, last_changed_dt.timestamp()))

            if cursor.fetchone():
                conn.close()
                return {"status": "skipped", "reason": "duplicate"}, 200
            else:
                # Insert new state with unit_of_measurement attribute
                attributes = json.dumps({"unit_of_measurement": "kWh"})
                cursor.execute("""
                    INSERT INTO states (
                        metadata_id, state, last_changed_ts, last_updated_ts, attributes
                    )
                    VALUES (?, ?, ?, ?, ?)
                """, (
                    metadata_id,
                    state,
                    last_changed_dt.timestamp(),
                    last_updated_dt.timestamp(),
                    attributes
                ))

            conn.commit()
            conn.close()

            return {"status": "success"}, 200

        except Exception as e:
            self.log(f"Database error: {str(e)}", level="ERROR")
            if 'conn' in locals():
                conn.close()
            return {"error": str(e)}, 500

    def generate_statistics(self, data, **kwargs):
        """Generate statistics from individual hourly consumption values"""
        entity_id = data.get("entity_id")

        if not entity_id:
            return {"error": "entity_id required"}, 400

        try:
            conn = sqlite3.connect('/homeassistant/home-assistant_v2.db', timeout=30)
            cursor = conn.cursor()

            # Get statistics metadata_id
            cursor.execute("SELECT id FROM statistics_meta WHERE statistic_id = ?", (entity_id,))
            row = cursor.fetchone()
            if not row:
                conn.close()
                return {"error": "No statistics metadata found"}, 404
            stats_metadata_id = row[0]

            # Get states metadata_id
            cursor.execute("SELECT metadata_id FROM states_meta WHERE entity_id = ?", (entity_id,))
            row = cursor.fetchone()
            if not row:
                conn.close()
                return {"error": "No states found"}, 404
            states_metadata_id = row[0]

            # Get all states in chronological order (individual hourly consumption)
            cursor.execute("""
                SELECT state, last_changed_ts
                FROM states
                WHERE metadata_id = ?
                AND last_changed_ts IS NOT NULL
                AND state NOT IN ('unknown', 'unavailable', '0.0', '0')
                AND CAST(state AS REAL) > 0
                ORDER BY last_changed_ts
            """, (states_metadata_id,))

            states = cursor.fetchall()

            if not states:
                conn.close()
                return {"error": "No valid states found"}, 404

            # Group by hour and sum consumption for each hour
            from collections import defaultdict
            hourly_data = defaultdict(list)

            for state_str, ts in states:
                consumption = float(state_str)
                hour_ts = int(ts // 3600 * 3600)
                hourly_data[hour_ts].append(consumption)

            # Calculate cumulative sum for statistics
            inserted = 0
            updated = 0
            cumulative_sum = 0.0

            for hour_ts in sorted(hourly_data.keys()):
                # Sum all consumption values for this hour
                hour_consumption = sum(hourly_data[hour_ts])
                cumulative_sum += hour_consumption

                # Check if exists
                cursor.execute("""
                    SELECT id FROM statistics
                    WHERE metadata_id = ? AND start_ts = ?
                """, (stats_metadata_id, hour_ts))

                if cursor.fetchone():
                    cursor.execute("""
                        UPDATE statistics
                        SET state = ?, sum = ?
                        WHERE metadata_id = ? AND start_ts = ?
                    """, (hour_consumption, cumulative_sum, stats_metadata_id, hour_ts))
                    updated += 1
                else:
                    cursor.execute("""
                        INSERT INTO statistics (metadata_id, start_ts, state, sum)
                        VALUES (?, ?, ?, ?)
                    """, (stats_metadata_id, hour_ts, hour_consumption, cumulative_sum))
                    inserted += 1

            conn.commit()
            conn.close()

            self.log(f"Generated statistics: {inserted} inserted, {updated} updated")

            return {
                "status": "success",
                "inserted": inserted,
                "updated": updated,
                "total_hours": len(hourly_data)
            }, 200

        except Exception as e:
            self.log(f"Statistics generation error: {str(e)}", level="ERROR")
            if 'conn' in locals():
                conn.close()
            return {"error": str(e)}, 500

    def generate_cost_statistics(self, data, **kwargs):
        """Generate cost statistics from energy usage statistics"""
        energy_entity_id = data.get("energy_entity_id")
        cost_entity_id = data.get("cost_entity_id")
        rate = data.get("rate")  # Optional - will auto-calculate if not provided

        if not energy_entity_id or not cost_entity_id:
            return {"error": "energy_entity_id and cost_entity_id required"}, 400

        try:
            conn = sqlite3.connect('/homeassistant/home-assistant_v2.db', timeout=30)
            cursor = conn.cursor()

            # Get energy statistics metadata_id
            cursor.execute("SELECT id FROM statistics_meta WHERE statistic_id = ?", (energy_entity_id,))
            row = cursor.fetchone()
            if not row:
                conn.close()
                return {"error": f"No statistics metadata found for {energy_entity_id}"}, 404
            energy_stats_id = row[0]

            # Get cost statistics metadata_id
            cursor.execute("SELECT id FROM statistics_meta WHERE statistic_id = ?", (cost_entity_id,))
            row = cursor.fetchone()
            if not row:
                conn.close()
                return {"error": f"No statistics metadata found for {cost_entity_id}"}, 404
            cost_stats_id = row[0]

            # Auto-calculate rate if not provided
            if not rate:
                self.log("Rate not provided, attempting to auto-calculate from existing cost statistics")

                # Get the most recent cost and energy statistics for the same timestamp
                cursor.execute("""
                    SELECT e.start_ts, e.state, c.state
                    FROM statistics e
                    JOIN statistics c ON e.start_ts = c.start_ts
                    WHERE e.metadata_id = ?
                    AND c.metadata_id = ?
                    AND e.state IS NOT NULL
                    AND c.state IS NOT NULL
                    AND e.state > 0
                    AND c.state > 0
                    ORDER BY e.start_ts DESC
                    LIMIT 1
                """, (energy_stats_id, cost_stats_id))

                rate_calc_row = cursor.fetchone()
                if rate_calc_row:
                    _, energy_kwh, hour_cost = rate_calc_row
                    rate = float(hour_cost) / float(energy_kwh)
                    self.log(f"Auto-calculated rate: {rate:.5f} (from energy={energy_kwh} kWh, cost={hour_cost})")
                else:
                    conn.close()
                    return {"error": "Could not auto-calculate rate: no existing cost statistics found. Please provide rate parameter."}, 400
            else:
                try:
                    rate = float(rate)
                    self.log(f"Using provided rate: {rate:.5f}")
                except ValueError:
                    conn.close()
                    return {"error": "rate must be a number"}, 400

            # Get all energy statistics in chronological order
            cursor.execute("""
                SELECT start_ts, state
                FROM statistics
                WHERE metadata_id = ?
                ORDER BY start_ts
            """, (energy_stats_id,))

            energy_stats = cursor.fetchall()

            if not energy_stats:
                conn.close()
                return {"error": "No energy statistics found"}, 404

            # Calculate cumulative cost
            inserted = 0
            updated = 0
            cumulative_cost = 0.0

            for start_ts, energy_kwh in energy_stats:
                if energy_kwh is None:
                    continue

                # Calculate cost for this hour
                hour_cost = float(energy_kwh) * rate
                cumulative_cost += hour_cost

                # Check if cost statistic exists
                cursor.execute("""
                    SELECT id FROM statistics
                    WHERE metadata_id = ? AND start_ts = ?
                """, (cost_stats_id, start_ts))

                if cursor.fetchone():
                    cursor.execute("""
                        UPDATE statistics
                        SET state = ?, sum = ?
                        WHERE metadata_id = ? AND start_ts = ?
                    """, (hour_cost, cumulative_cost, cost_stats_id, start_ts))
                    updated += 1
                else:
                    cursor.execute("""
                        INSERT INTO statistics (metadata_id, start_ts, state, sum)
                        VALUES (?, ?, ?, ?)
                    """, (cost_stats_id, start_ts, hour_cost, cumulative_cost))
                    inserted += 1

            conn.commit()
            conn.close()

            self.log(f"Generated cost statistics: {inserted} inserted, {updated} updated")

            return {
                "status": "success",
                "inserted": inserted,
                "updated": updated,
                "total_hours": len(energy_stats),
                "total_cost": cumulative_cost,
                "rate_used": rate
            }, 200

        except Exception as e:
            self.log(f"Cost statistics generation error: {str(e)}", level="ERROR")
            if 'conn' in locals():
                conn.close()
            return {"error": str(e)}, 500
