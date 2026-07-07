import csv
import json
import os

source_file = r"c:\Users\lenovo\Desktop\LeMiCi\Camunda-Workers\docs\Plans\Association\Data\Indian_Associations_Final_Mapped.csv"
v2_data_dir = r"c:\Users\lenovo\Desktop\LeMiCi\Camunda-Workers\data\postgres\v2-data"

listings_file = os.path.join(v2_data_dir, "listings.csv")
cities_file = os.path.join(v2_data_dir, "franchise_cities.csv")
assoc_file = os.path.join(v2_data_dir, "associations.csv")

# Load DB listings by name
db_listings = {}
with open(listings_file, 'r', encoding='utf-8') as f:
    reader = csv.DictReader(f)
    for row in reader:
        db_listings[row['name'].strip()] = row

# Load cities
db_cities = {}
with open(cities_file, 'r', encoding='utf-8') as f:
    reader = csv.reader(f)
    next(reader) # skip header
    for row in reader:
        if len(row) >= 3:
            listing_id = row[1]
            city = row[2]
            db_cities[listing_id] = city

# Load associations
db_assocs = {}
with open(assoc_file, 'r', encoding='utf-8') as f:
    reader = csv.DictReader(f)
    for row in reader:
        db_assocs[row['listing_id']] = row

# Verification results
mismatches = []
missing_in_db = []

with open(source_file, 'r', encoding='utf-8') as f:
    reader = csv.DictReader(f)
    for row in reader:
        name = row['name'].strip()
        if name not in db_listings:
            missing_in_db.append(name)
            continue
            
        db_listing = db_listings[name]
        l_id = db_listing['id']
        
        # Check website
        src_web = row['website'].strip()
        db_web = db_listing.get('website_url', '').strip()
        if src_web != db_web:
            mismatches.append(f"[{name}] Website mismatch: Source='{src_web}', DB='{db_web}'")
            
        # Check location
        src_city = row['city'].strip()
        db_city = db_cities.get(l_id, '').strip()
        if src_city != db_city:
            mismatches.append(f"[{name}] Location mismatch: Source='{src_city}', DB='{db_city}'")
            
        # Check CEO name / metadata
        # DB association file does not even have metadata!
        assoc_row = db_assocs.get(l_id)
        if not assoc_row:
            mismatches.append(f"[{name}] Missing from associations.csv entirely!")
        else:
            # Check if json_data column is anywhere in DB
            pass

print("=== MISSING IN DB ===")
print(f"Total: {len(missing_in_db)}")
if len(missing_in_db) > 0:
    print(missing_in_db[:5], "...")

print("\n=== MISMATCHES ===")
print(f"Total: {len(mismatches)}")
for m in mismatches[:20]:
    print(m)
