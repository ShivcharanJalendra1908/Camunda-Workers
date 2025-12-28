# Franchise PostgreSQL Worker

A unified Camunda worker that handles all PostgreSQL operations for franchise management across 4 tables.

## 📋 Features

- **Single Worker, Multiple Operations**: One worker handles all CRUD operations
- **Operation-Based Routing**: Uses `operation_type` to determine which action to perform
- **Transaction Support**: Ensures data consistency across operations
- **Audit Trail**: Automatic tracking of `created_by` and `updated_by` with UUID validation
- **Role-Based Access**: Enforces user vs admin permissions

## 🗂️ Supported Tables

1. **franchises** - Main franchise information
2. **franchise_business_overview** - Products & Services
3. **franchise_investment_requirement** - Investment details
4. **franchise_operations** - Space, Staff, Support

## 🔧 Operations Supported

### Franchise Operations
- `CREATE_FRANCHISE` - Create new franchise
- `UPDATE_FRANCHISE` - Update franchise (Admin only)
- `GET_FRANCHISE` - Get franchise by ID or slug
- `DELETE_FRANCHISE` - Delete franchise (Admin only)
- `GET_FULL_FRANCHISE` - Get franchise with all related data

### Business Overview Operations
- `CREATE_BUSINESS_OVERVIEW` - Add products/services
- `UPDATE_BUSINESS_OVERVIEW` - Update products/services (Admin only)

### Investment Operations
- `CREATE_INVESTMENT` - Add investment requirements
- `UPDATE_INVESTMENT` - Update investment details (Admin only)

### Operations Management
- `CREATE_OPERATIONS` - Add operational details
- `UPDATE_OPERATIONS` - Update operational info (Admin only)

## 📦 Installation

1. **Copy the worker files:**
```bash
mkdir -p internal/workers/data-access/franchise-postgres
cp handler.go models.go config.go internal/workers/data-access/franchise-postgres/
```

2. **Update your worker-manager:**
Add the worker registration code from `worker-registration-example.go`

3. **Deploy the BPMN:**
```bash
cp franchise-management.bpmn bpmn/
```

## 🚀 Usage Examples

### Example 1: Create Complete Franchise (User Action)

**Workflow Variables:**
```json
{
  "name": "McDonald's",
  "slug": "mcdonalds",
  "founded_year": 1955,
  "industry": "Food & Beverage",
  "description": "Global fast-food chain",
  "user_id": "550e8400-e29b-41d4-a716-446655440000",
  "products": [
    {
      "name": "Big Mac",
      "description": "Signature burger",
      "category": "Burgers"
    }
  ],
  "services": [
    {
      "name": "Drive-through",
      "type": "Quick Service"
    }
  ],
  "initial_investment_min": 1000000,
  "initial_investment_max": 2200000,
  "franchise_fee": 45000,
  "space_min_sqft": 2000,
  "training_provided": true
}
```

**BPMN Service Task:**
```xml
<bpmn:serviceTask id="CreateFranchise" name="Create Franchise">
  <bpmn:extensionElements>
    <zeebe:taskDefinition type="franchise-postgres" />
    <zeebe:ioMapping>
      <zeebe:input source="='CREATE_FRANCHISE'" target="operation_type" />
      <zeebe:input source="=name" target="name" />
      <zeebe:input source="=slug" target="slug" />
      <zeebe:input source="=user_id" target="created_by" />
      <zeebe:output source="=franchise_id" target="franchise_id" />
    </zeebe:ioMapping>
  </bpmn:extensionElements>
</bpmn:serviceTask>
```

### Example 2: Admin Update Franchise

**Workflow Variables:**
```json
{
  "operation_type": "UPDATE_FRANCHISE",
  "franchise_id": "123e4567-e89b-12d3-a456-426614174000",
  "updated_by": "admin-uuid-here",
  "trusted_seller": true,
  "description": "Updated description by admin"
}
```

### Example 3: Get Full Franchise Details

**Workflow Variables:**
```json
{
  "operation_type": "GET_FULL_FRANCHISE",
  "slug": "mcdonalds"
}
```

**Output:**
```json
{
  "franchise": {
    "id": "123e4567-e89b-12d3-a456-426614174000",
    "name": "McDonald's",
    "slug": "mcdonalds",
    "created_by": "550e8400-e29b-41d4-a716-446655440000",
    "updated_by": null
  },
  "business_overview": {
    "products": [...],
    "services": [...]
  },
  "investment": {
    "initial_investment_min": 1000000,
    "franchise_fee": 45000
  },
  "operations": {
    "space_min_sqft": 2000,
    "training_provided": true
  }
}
```

## 🔐 Security & Authorization

### User Permissions (created_by)
- ✅ Can CREATE franchise records
- ❌ Cannot UPDATE franchise records
- ❌ Cannot DELETE franchise records

### Admin Permissions (updated_by)
- ✅ Can UPDATE any franchise
- ✅ Can DELETE franchises
- ✅ Can modify sensitive fields (trusted_seller, etc.)

### JWT Token Flow
1. User/Admin logs in via Keycloak
2. JWT token contains:
   - `sub` = User/Admin UUID
   - `realm_access.roles` = ["USER"] or ["SYSTEM_ADMIN"]
3. Backend extracts UUID from token
4. Worker validates and uses UUID for `created_by` or `updated_by`

## 🏗️ Architecture

```
API Request (JWT Token)
    ↓
Backend validates token & extracts UUID
    ↓
Starts Camunda Workflow
    ↓
franchise-postgres Worker
    ↓
PostgreSQL Transaction
    ↓
Returns Result to Workflow
```

## 📊 Database Indexes

The worker efficiently uses existing indexes:
- `idx_franchises_slug` - For slug-based lookups
- `idx_franchises_industry` - For industry filtering
- `idx_franchises_created_by` - For audit queries

## ⚙️ Configuration

```yaml
# config.yaml
franchise_postgres:
  worker_name: "franchise-postgres-worker"
  job_type: "franchise-postgres"
  max_jobs_active: 10
  poll_interval: 100ms
  request_timeout: 30s
  db_max_retries: 3
  tx_timeout: 10s
  enable_audit_log: true
```

## 🧪 Testing

### Unit Tests
```bash
go test ./internal/workers/data-access/franchise-postgres/... -v
```

### Integration Tests
```bash
# Start test environment
docker-compose -f deployments/docker/docker-compose.yml up -d

# Run integration tests
go test ./test/e2e/... -tags=integration -v
```

## 🐛 Error Handling

The worker handles these error scenarios:
- Invalid UUID format
- Missing required fields
- Duplicate slug violation
- Foreign key violations
- Transaction failures
- Database connection issues

**Example Error Response:**
```json
{
  "success": false,
  "error_code": "VALIDATION_ERROR",
  "message": "slug is required"
}
```

## 📝 Logging

The worker logs:
- All operation attempts
- Success/failure with timing
- Transaction boundaries
- Error details with context

**Example Log:**
```json
{
  "level": "info",
  "msg": "Successfully completed operation",
  "operation": "CREATE_FRANCHISE",
  "franchise_id": "123e4567-e89b-12d3-a456-426614174000",
  "duration_ms": 45
}
```

## 🔄 Migration from Multiple Workers

If you previously had separate workers:
1. Update BPMN files to use single `franchise-postgres` job type
2. Add `operation_type` to all service tasks
3. Remove old worker registrations
4. Deploy updated BPMN models

## 📚 Related Documentation

- [PostgreSQL Schema](../../docs/database-schema.md)
- [Authentication Flow](../../docs/auth-guide.md)
- [BPMN Best Practices](../../docs/bpmn-patterns.md)
- [Worker Development Guide](../../docs/development-guide.md)

## 🤝 Contributing

1. Follow the existing code structure
2. Add comprehensive tests
3. Update this README with new operations
4. Ensure backward compatibility

## 📄 License

[Your License Here]