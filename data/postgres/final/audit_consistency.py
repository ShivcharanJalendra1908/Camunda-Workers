import csv
import os

directory = r'c:\Users\lenovo\Desktop\LeMiCi\Camunda-Workers\data\postgres\final'
franchise_file = os.path.join(directory, 'franchises.csv')

def get_column_data(file_path, column_name):
    data = []
    if not os.path.exists(file_path):
        return None
    with open(file_path, mode='r', encoding='utf-8') as f:
        reader = csv.DictReader(f)
        for row in reader:
            if column_name in row:
                data.append(row[column_name])
    return data

# Load main franchise IDs
franchise_ids_list = get_column_data(franchise_file, 'id')
if franchise_ids_list is None:
    print(f"Error: {franchise_file} not found.")
    exit(1)

franchise_ids = set(franchise_ids_list)
print(f"Total Franchises in franchises.csv: {len(franchise_ids)}")

other_files = [
    'franchise_business_overview.csv',
    'franchise_categories.csv',
    'franchise_investment_requirement.csv',
    'franchise_operations.csv',
    'franchise_stats.csv',
    'franchise_social_links.csv',
    'franchise_cities.csv'
]

for file_name in other_files:
    file_path = os.path.join(directory, file_name)
    if not os.path.exists(file_path):
        print(f"\nFile {file_name} not found.")
        continue
    
    table_ids_list = get_column_data(file_path, 'franchise_id')
    
    if table_ids_list is None:
        print(f"\nColumn 'franchise_id' not found in {file_name}")
        continue
        
    table_ids = set(table_ids_list)
    
    orphans = table_ids - franchise_ids
    missing_in_table = franchise_ids - table_ids
    
    print(f"\nAnalysis for {file_name}:")
    print(f"  Total records (rows): {len(table_ids_list)}")
    print(f"  Unique franchise_ids: {len(table_ids)}")
    
    if orphans:
        print(f"  Orphans (in {file_name} but not in franchises.csv): {len(orphans)}")
        print(f"    IDs: {list(orphans)}")
    else:
        print(f"  No orphans found.")
        
    if missing_in_table:
        print(f"  Missing (in franchises.csv but not in {file_name}): {len(missing_in_table)}")
        if len(missing_in_table) > 10:
            print(f"    IDs (first 10): {list(missing_in_table)[:10]}...")
        else:
            print(f"    IDs: {list(missing_in_table)}")
    else:
        print(f"  All franchise_ids from franchises.csv are present.")
