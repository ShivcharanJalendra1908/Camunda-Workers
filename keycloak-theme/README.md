# Keycloak Theme Installation Guide

To use the custom **LeMiCi** theme in your Dockerized Keycloak, follow these steps:

## 1. Copy the theme files
You need to copy the `keycloak-theme/lemici` folder into the Keycloak container's `themes` directory.

If you are using `docker-compose`, you can add a volume mapping:
```yaml
services:
  keycloak:
    ...
    volumes:
      - ./keycloak-theme/lemici:/opt/keycloak/themes/lemici
```

If you are running manually via `docker run`:
```bash
docker cp ./keycloak-theme/lemici <container_id>:/opt/keycloak/themes/
```

## 2. Activate the theme
1. Log in to the Keycloak Admin Console (`http://localhost:8080/admin`).
2. Select your realm (**lemici**).
3. Go to **Realm Settings** -> **Themes**.
4. Set **Login Theme** to `lemici`.
5. Save changes.

## 3. Configure Login Options
To show the "Email Login" and "Social Providers" (Google, etc.) as you requested:
1. Go to **Authentication** -> **Flows**.
2. Ensure **Browser** flow is configured to allow `Identity Provider Redirector` and `Username Password Form`.
3. To add Google/LinkedIn:
   - Go to **Identity Providers**.
   - Add **Google** and **LinkedIn** and configure your Client IDs/Secrets from their respective developer consoles.
