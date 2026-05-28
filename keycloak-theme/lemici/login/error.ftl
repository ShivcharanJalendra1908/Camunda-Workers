<#import "template.ftl" as layout>
<@layout.registrationLayout displayMessage=true; section>
    <#if section = "header">
        <div class="login-header">
            <h1>Authentication Error</h1>
            <p>Something went wrong during sign in.</p>
        </div>
    <#elseif section = "form">
        <div id="kc-error-message">
            <#if client?? && client.baseUrl?has_content>
                <div style="text-align: center; margin-top: 20px;">
                    <p><a id="backToApplication" href="${client.baseUrl}" class="pf-c-button pf-m-primary" style="text-decoration: none; display: inline-block; padding: 10px 20px; background: #6D3E93; color: white; border-radius: 8px; font-weight: 600;">
                        ${kcSanitize(msg("backToApplication"))?no_esc}
                    </a></p>
                </div>
            <#else>
                <div style="text-align: center; margin-top: 20px;">
                    <a href="${url.loginUrl}" onclick="event.preventDefault(); handleReturnToLogin();" style="color: #6D3E93; font-weight: 600; text-decoration: none;">Return to Login</a>
                </div>
                <script>
                    function handleReturnToLogin() {
                        var host = window.location.hostname;
                        if (host === 'us-dev-api.lemici.com') {
                            window.location.href = 'https://dev.lemici.com/';
                        } else if (host === 'dev-api.lemici.com') {
                            window.location.href = 'https://lemici.com/';
                        } else if (host.indexOf('demo-api') !== -1) {
                            window.location.href = 'https://demo.lemici.com/';
                        } else if (host === 'localhost' || host === '127.0.0.1' || host.indexOf('192.168.') === 0) {
                            window.location.href = 'http://localhost:3000/';
                        } else {
                            window.location.href = '${url.loginUrl}';
                        }
                    }
                </script>
            </#if>
        </div>
    </#if>
</@layout.registrationLayout>