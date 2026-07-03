import csv

file_path = 'data/postgres/v2-data/listings.csv'

rows = []
header = []
updated = 0
with open(file_path, mode='r', newline='', encoding='utf-8') as f:
    reader = csv.reader(f)
    header = next(reader)
    
    verified_idx = header.index('verified')
    
    for row in reader:
        if row[verified_idx] in ['true', 't', '1', 'TRUE']:
            row[verified_idx] = 'false'
            updated += 1
        elif row[verified_idx] == '':
            row[verified_idx] = 'false'
            updated += 1
        rows.append(row)

with open(file_path, mode='w', newline='', encoding='utf-8') as f:
    writer = csv.writer(f)
    writer.writerow(header)
    writer.writerows(rows)

print(f"Updated {updated} records to verified = false in listings.csv")
