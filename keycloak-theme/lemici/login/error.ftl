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
                    <a href="${url.loginUrl}" style="color: #6D3E93; font-weight: 600; text-decoration: none;">Return to Login</a>
                </div>
            </#if>
        </div>
    </#if>
</@layout.registrationLayout>